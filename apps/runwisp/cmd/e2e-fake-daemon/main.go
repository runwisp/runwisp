// Command e2e-fake-daemon impersonates the daemon for a single execution
// against a live cloud stack. It connects to Hub, waits for one
// `execution:dispatch`, performs the gzip+PUT archival to the supplied
// `logUploadUrl`, then sends the terminal `execution:update` with the
// returned `logPath`+`logSize`.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/runwisp/runwisp/internal/cloud/logarchive"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fake daemon failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	wsURL := flag.String("ws", "ws://127.0.0.1:18788/api/v1/runner/ws", "Hub WebSocket URL")
	token := flag.String("token", "", "Runner token (rt_...)")
	fingerprint := flag.String("fingerprint", "e2e-fp-001", "Runner fingerprint")
	logBody := flag.String("log", "hello-from-e2e\n", "Log body to archive")
	flag.Parse()

	if *token == "" {
		return fmt.Errorf("--token is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+*token)
	headers.Set("X-Runner-Fingerprint", *fingerprint)
	headers.Set("X-Runner-Agent-Version", "e2e-fake-daemon/0.1")

	conn, _, err := websocket.Dial(ctx, *wsURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	conn.SetReadLimit(4 * 1024 * 1024)
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read auth:result.
	authResultRaw, err := readMsg(ctx, conn)
	if err != nil {
		return fmt.Errorf("read auth result: %w", err)
	}
	var authResult struct {
		Type         string `json:"type"`
		Success      bool   `json:"success"`
		ConnectionID string `json:"connectionId"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(authResultRaw, &authResult); err != nil {
		return fmt.Errorf("decode auth result: %w", err)
	}
	if !authResult.Success {
		return fmt.Errorf("auth failed: %s", authResult.Error)
	}
	slog.Info("authenticated", "connectionId", authResult.ConnectionID)

	// Read up to ~30 seconds for an execution:dispatch frame, ignoring pings.
	dispatchDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(dispatchDeadline) {
		raw, err := readMsg(ctx, conn)
		if err != nil {
			return fmt.Errorf("read dispatch: %w", err)
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		if head.Type != "execution:dispatch" {
			slog.Info("ignored frame", "type", head.Type)
			continue
		}
		return handleDispatch(ctx, conn, raw, *logBody)
	}
	return fmt.Errorf("no dispatch within deadline")
}

func handleDispatch(ctx context.Context, conn *websocket.Conn, raw []byte, logBody string) error {
	var disp struct {
		Type      string `json:"type"`
		Execution struct {
			ExecutionID  string `json:"executionId"`
			LogUploadURL string `json:"logUploadUrl"`
			LogPath      string `json:"logPath"`
			Timeout      int    `json:"timeout"`
		} `json:"execution"`
	}
	if err := json.Unmarshal(raw, &disp); err != nil {
		return fmt.Errorf("decode dispatch: %w", err)
	}
	if disp.Execution.ExecutionID == "" {
		return fmt.Errorf("dispatch missing executionId: %s", string(raw))
	}
	slog.Info("dispatch received",
		"executionId", disp.Execution.ExecutionID,
		"logUploadUrl_present", disp.Execution.LogUploadURL != "",
		"logPath", disp.Execution.LogPath,
	)

	startedAt := time.Now().UTC()
	startedAtStr := startedAt.Format(time.RFC3339Nano)
	runningRaw, _ := json.Marshal(map[string]any{
		"type":        "execution:update",
		"v":           2,
		"sentAt":      startedAtStr,
		"executionId": disp.Execution.ExecutionID,
		"status":      "running",
		"startedAt":   startedAtStr,
	})
	if err := conn.Write(ctx, websocket.MessageText, runningRaw); err != nil {
		return fmt.Errorf("send running update: %w", err)
	}

	// Materialise the log on disk in a temp file so we exercise the
	// real logarchive.Archive code path.
	dir, err := os.MkdirTemp("", "e2e-daemon-log-")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(dir)
	logFilePath := filepath.Join(dir, "execution.log")
	if err := os.WriteFile(logFilePath, []byte(logBody), 0o644); err != nil {
		return fmt.Errorf("write log: %w", err)
	}

	logPath, logSize, err := archiveExecutionLog(ctx, disp.Execution.LogUploadURL, disp.Execution.LogPath, logFilePath)
	if err != nil {
		return err
	}

	return sendTerminalUpdate(ctx, conn, disp.Execution.ExecutionID, startedAtStr, logPath, logSize)
}

func archiveExecutionLog(ctx context.Context, uploadURL, cloudLogPath, localLogPath string) (string, int64, error) {
	if uploadURL == "" {
		slog.Warn("dispatch has empty logUploadUrl; skipping archive")
		return "", 0, nil
	}
	size, err := logarchive.Archive(ctx, http.DefaultClient, uploadURL, localLogPath)
	if err != nil {
		return "", 0, fmt.Errorf("archive: %w", err)
	}
	slog.Info("archive uploaded", "logPath", cloudLogPath, "logSize", size)
	return cloudLogPath, size, nil
}

func sendTerminalUpdate(ctx context.Context, conn *websocket.Conn, executionID, startedAtStr, logPath string, logSize int64) error {
	finishedAt := time.Now().UTC()
	finishedAtStr := finishedAt.Format(time.RFC3339Nano)
	payload := map[string]any{
		"type":        "execution:update",
		"v":           2,
		"sentAt":      finishedAtStr,
		"executionId": executionID,
		"status":      "ok",
		"exitCode":    0,
		"startedAt":   startedAtStr,
		"finishedAt":  finishedAtStr,
	}
	if logPath != "" {
		payload["logPath"] = logPath
		payload["logSize"] = logSize
	}
	raw, _ := json.Marshal(payload)
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return fmt.Errorf("send terminal: %w", err)
	}
	slog.Info("terminal sent", "logPathSent", logPath != "")

	// Drain any trailing frames briefly so the server sees the message.
	drain, dcancel := context.WithTimeout(ctx, 1*time.Second)
	defer dcancel()
	for {
		if _, err := readMsg(drain, conn); err != nil {
			break
		}
	}
	return nil
}

func readMsg(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	// Some frames may have base64 padding etc; we just return raw JSON bytes.
	if !looksLikeJSON(raw) {
		// Decode if base64 (defensive; current protocol is plain JSON).
		if dec, err := base64.StdEncoding.DecodeString(string(raw)); err == nil {
			return dec, nil
		}
	}
	return raw, nil
}

func looksLikeJSON(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}
