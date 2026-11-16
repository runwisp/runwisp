// Command e2e-fake-daemon impersonates the daemon for a single execution
// against a live station stack, over protocol runwisp.v1. It says hello,
// waits for one execution:dispatch, accepts it, reports it running, uploads
// its log to the target an upload:request returns, reports it finished with
// the archive, and waits for the station to ack every update.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/coder/websocket"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/logarchive"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fake daemon failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	wsURL := flag.String("ws", "ws://127.0.0.1:18788/api/v1/runner/ws", "Station WebSocket URL")
	token := flag.String("token", "", "Runner token (rt_...)")
	fingerprint := flag.String("fingerprint", "e2e-fp-001", "Instance id sent in hello")
	logBody := flag.String("log", "hello-from-e2e\n", "Log body to archive")
	flag.Parse()

	if *token == "" {
		return errors.New("--token is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+*token)
	conn, _, err := websocket.Dial(ctx, *wsURL, &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{"runwisp.v1"}})
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if conn.Subprotocol() != "runwisp.v1" {
		return errors.New("station did not select runwisp.v1")
	}
	conn.SetReadLimit(4 << 20)

	d := &fakeDaemon{conn: conn, frames: make(chan protocol.Envelope, 64)}
	go d.readLoop(ctx)
	return d.session(ctx, *fingerprint, *logBody)
}

type fakeDaemon struct {
	conn   *websocket.Conn
	frames chan protocol.Envelope
	seq    int64
	ids    int
}

// readLoop hands every inbound frame to the main goroutine, which does all
// the writing.
func (d *fakeDaemon) readLoop(ctx context.Context) {
	defer close(d.frames)
	for {
		_, raw, err := d.conn.Read(ctx)
		if err != nil {
			return
		}
		var env protocol.Envelope
		if json.Unmarshal(raw, &env) == nil {
			d.frames <- env
		}
	}
}

// handshake says hello, waits for welcome and syncs tasks if asked.
func (d *fakeDaemon) handshake(ctx context.Context, instanceID string) error {
	host, _ := os.Hostname()
	hello := protocol.Hello{
		InstanceID: instanceID,
		Agent:      &protocol.AgentInfo{Version: "0.0.0-e2e", Os: runtime.GOOS, Arch: runtime.GOARCH, Host: host},
		Features:   map[string]*protocol.FeatureStatus{"exec.shell": {Available: true}},
		Labels:     map[string]string{},
		Executions: []protocol.InventoryEntry{},
	}
	if err := d.write(ctx, "hello", "", "", hello); err != nil {
		return err
	}
	welcomeEnv, err := d.await(ctx, func(e protocol.Envelope) bool { return e.Type == "welcome" })
	if err != nil {
		return fmt.Errorf("wait for welcome: %w", err)
	}
	var welcome protocol.Welcome
	_ = json.Unmarshal(welcomeEnv.Data, &welcome)
	slog.Info("welcomed", "connectionId", welcome.ConnectionID)
	if !welcome.TasksHashKnown {
		if err := d.write(ctx, "tasks:sync", d.nextID(), "", protocol.TasksSync{Tasks: []protocol.TaskDefinition{}}); err != nil {
			return err
		}
	}
	return nil
}

func (d *fakeDaemon) session(ctx context.Context, instanceID, logBody string) error {
	if err := d.handshake(ctx, instanceID); err != nil {
		return err
	}
	dispatchEnv, err := d.await(ctx, func(e protocol.Envelope) bool { return e.Type == "execution:dispatch" })
	if err != nil {
		return fmt.Errorf("wait for dispatch: %w", err)
	}
	var dispatch protocol.ExecutionDispatch
	if err := json.Unmarshal(dispatchEnv.Data, &dispatch); err != nil || dispatch.ExecutionID == "" {
		return fmt.Errorf("bad dispatch: %s", dispatchEnv.Data)
	}
	slog.Info("dispatch received", "executionId", dispatch.ExecutionID)
	if err := d.write(ctx, "result", "", dispatchEnv.ID, protocol.Result{Ok: true}); err != nil {
		return err
	}

	startedAt := time.Now().UTC()
	if err := d.update(ctx, protocol.ExecutionUpdate{ExecutionID: dispatch.ExecutionID, Status: protocol.ExecutionStatusRunning, StartedAt: &startedAt}); err != nil {
		return err
	}
	finishedAt := time.Now().UTC()
	finished := protocol.ExecutionUpdate{
		ExecutionID: dispatch.ExecutionID,
		Status:      protocol.ExecutionStatusFinished,
		StartedAt:   &startedAt,
		FinishedAt:  &finishedAt,
		Reason:      "succeeded",
		IsFailure:   new(false),
		ExitCode:    new(0),
	}
	if err := d.update(ctx, finished); err != nil {
		return err
	}

	archived, err := d.archive(ctx, dispatch.ExecutionID, logBody)
	if err != nil {
		return err
	}
	if archived != nil {
		finished.Log = archived
		if err := d.update(ctx, finished); err != nil {
			return err
		}
	}

	if _, err := d.await(ctx, func(e protocol.Envelope) bool {
		var ack protocol.Ack
		return e.Type == "ack" && json.Unmarshal(e.Data, &ack) == nil && ack.Seq >= d.seq
	}); err != nil {
		return fmt.Errorf("wait for ack of seq %d: %w", d.seq, err)
	}
	slog.Info("every update acked", "seq", d.seq)
	return d.write(ctx, "goodbye", "", "", protocol.Goodbye{Code: protocol.GoodbyeCodeShutdown, Retry: true})
}

// archive asks for a log.archive target and uploads the log there; nil when
// the station declines.
func (d *fakeDaemon) archive(ctx context.Context, executionID, logBody string) (*protocol.LogArchive, error) {
	id := d.nextID()
	if err := d.write(ctx, "upload:request", id, "", protocol.UploadRequest{ExecutionID: executionID, Purpose: protocol.UploadPurposeLogArchive}); err != nil {
		return nil, err
	}
	env, err := d.await(ctx, func(e protocol.Envelope) bool { return e.Type == "result" && e.ReplyTo == id })
	if err != nil {
		return nil, fmt.Errorf("wait for upload targets: %w", err)
	}
	var res struct {
		protocol.Result
		protocol.UploadRequestResult
	}
	if err := json.Unmarshal(env.Data, &res); err != nil || !res.Ok {
		return nil, fmt.Errorf("upload:request refused: %s", env.Data)
	}
	if len(res.Uploads) == 0 {
		slog.Warn("station declined the log archive")
		return nil, nil
	}
	target := res.Uploads[0]

	dir, err := os.MkdirTemp("", "e2e-daemon-log-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(dir)
	logFilePath := filepath.Join(dir, "execution.log")
	if err := os.WriteFile(logFilePath, []byte(logBody), 0o600); err != nil {
		return nil, fmt.Errorf("write log: %w", err)
	}
	size, err := logarchive.Archive(ctx, http.DefaultClient, logarchive.Target{URL: target.URL, Method: target.Method, Headers: target.Headers}, logFilePath, true)
	if err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	slog.Info("archive uploaded", "key", target.Key, "bytes", size)
	return &protocol.LogArchive{Key: target.Key, Bytes: size}, nil
}

func (d *fakeDaemon) update(ctx context.Context, u protocol.ExecutionUpdate) error {
	d.seq++
	u.Seq = d.seq
	return d.write(ctx, "execution:update", "", "", u)
}

// await returns the first frame match accepts, answering everything else that
// needs it: a result for other commands, nothing for the rest.
func (d *fakeDaemon) await(ctx context.Context, match func(protocol.Envelope) bool) (protocol.Envelope, error) {
	ping := time.NewTicker(10 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return protocol.Envelope{}, ctx.Err()
		case <-ping.C:
			if err := d.write(ctx, "ping", "", "", nil); err != nil {
				return protocol.Envelope{}, err
			}
		case env, ok := <-d.frames:
			switch {
			case !ok:
				return protocol.Envelope{}, errors.New("connection closed")
			case match(env):
				return env, nil
			case env.Type == "goodbye":
				return protocol.Envelope{}, fmt.Errorf("station said goodbye: %s", env.Data)
			case env.ID != "":
				slog.Info("refusing command", "type", env.Type)
				refusal := protocol.Result{Error: &protocol.ErrorInfo{Code: protocol.ErrorCodeUnsupported, Message: "fake daemon"}}
				if err := d.write(ctx, "result", "", env.ID, refusal); err != nil {
					return protocol.Envelope{}, err
				}
			default:
				slog.Info("ignored frame", "type", env.Type)
			}
		}
	}
}

func (d *fakeDaemon) nextID() string {
	d.ids++
	return fmt.Sprintf("e2e-%d", d.ids)
}

func (d *fakeDaemon) write(ctx context.Context, typ, id, replyTo string, data any) error {
	env := protocol.Envelope{Type: typ, ID: id, ReplyTo: replyTo, SentAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		env.Data = raw
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := d.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return fmt.Errorf("send %s: %w", typ, err)
	}
	return nil
}
