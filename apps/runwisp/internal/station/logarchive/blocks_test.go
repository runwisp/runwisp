// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logarchive

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockedLog is about 2.5 blocks of lines of varying length, so no block
// boundary falls on a newline by accident.
func blockedLog() string {
	var b strings.Builder
	for i := 0; b.Len() < blockMinRaw*5/2; i++ {
		fmt.Fprintf(&b, "%d request id=%08x took %dms %s\n", i, i*2654435761, i%997, strings.Repeat("x", i%113))
	}
	return b.String()
}

// parseRB reads the RB table out of a gzip header's extra field.
func parseRB(t *testing.T, extra []byte) []archiveBlock {
	t.Helper()
	require.GreaterOrEqual(t, len(extra), 4)
	require.Equal(t, "RB", string(extra[:2]))
	data := extra[4:]
	require.Len(t, data, int(binary.LittleEndian.Uint16(extra[2:4])))
	require.Equal(t, byte(rbVersion), data[0])
	data = data[1:]
	var blocks []archiveBlock
	for len(data) > 0 {
		compressed, n := binary.Uvarint(data)
		require.Positive(t, n)
		raw, m := binary.Uvarint(data[n:])
		require.Positive(t, m)
		blocks = append(blocks, archiveBlock{compressed: int64(compressed), raw: int64(raw)})
		data = data[n+m:]
	}
	return blocks
}

func TestGzipFile_BlocksInflateOnTheirOwn(t *testing.T) {
	content := blockedLog()
	f, size, err := gzipFile(writeLog(t, content))
	require.NoError(t, err)
	defer f.Close()
	archive, err := io.ReadAll(io.NewSectionReader(f, 0, size))
	require.NoError(t, err)

	// One member: Go's reader, told not to look for more, reads it all.
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	zr.Multistream(false)
	got, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.Equal(t, content, string(got))
	assert.Empty(t, zr.Name)
	assert.Empty(t, zr.Comment)

	blocks := parseRB(t, zr.Extra)
	require.Len(t, blocks, 3)
	headerLen := 10 + 2 + len(zr.Extra)
	off, rawOff := int64(headerLen), int64(0)
	for i, b := range blocks {
		want := content[rawOff : rawOff+b.raw]
		if i < len(blocks)-1 {
			assert.GreaterOrEqual(t, b.raw, int64(blockMinRaw))
			assert.True(t, strings.HasSuffix(want, "\n"), "block %d ends at a line end", i)
		}
		fr := flate.NewReader(bytes.NewReader(archive[off : off+b.compressed]))
		buf := make([]byte, b.raw)
		_, err := io.ReadFull(fr, buf)
		require.NoError(t, err, "block %d", i)
		assert.Equal(t, want, string(buf), "block %d", i)
		off += b.compressed
		rawOff += b.raw
	}
	assert.Equal(t, int64(len(content)), rawOff)
	assert.Equal(t, size, off+8, "the trailer follows the last block")
}

func TestGzipFile_GunzipReadsItByteForByte(t *testing.T) {
	gz, err := exec.LookPath("gzip")
	if err != nil {
		t.Skip("gzip not installed")
	}
	content := blockedLog()
	f, size, err := gzipFile(writeLog(t, content))
	require.NoError(t, err)
	defer f.Close()

	cmd := exec.Command(gz, "-dc")
	cmd.Stdin = io.NewSectionReader(f, 0, size)
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.True(t, string(out) == content, "gunzip output differs from the log")
}

func TestGzipFile_SmallLogIsOneBlock(t *testing.T) {
	for _, content := range []string{"", "one line\n", "no trailing newline"} {
		f, size, err := gzipFile(writeLog(t, content))
		require.NoError(t, err)
		zr, err := gzip.NewReader(io.NewSectionReader(f, 0, size))
		require.NoError(t, err)
		got, err := io.ReadAll(zr)
		require.NoError(t, err)
		assert.Equal(t, content, string(got))
		blocks := parseRB(t, zr.Extra)
		require.Len(t, blocks, 1)
		assert.Equal(t, int64(len(content)), blocks[0].raw)
		assert.Equal(t, size, int64(10+2+len(zr.Extra))+blocks[0].compressed+8)
		_ = f.Close()
	}
}

func TestGzipHeader_DropsATableTooBigForFEXTRA(t *testing.T) {
	blocks := make([]archiveBlock, 10000)
	for i := range blocks {
		blocks[i] = archiveBlock{compressed: 1 << 20, raw: blockMinRaw}
	}
	header := gzipHeader(blocks)
	assert.Len(t, header, 10)
	assert.Zero(t, header[3], "no FEXTRA flag")

	zr, err := gzip.NewReader(io.MultiReader(bytes.NewReader(header), emptyDeflate(t)))
	require.NoError(t, err)
	assert.Empty(t, zr.Extra)
}

// emptyDeflate is a deflate stream of nothing followed by its gzip trailer.
func emptyDeflate(t *testing.T) io.Reader {
	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.DefaultCompression)
	require.NoError(t, err)
	require.NoError(t, fw.Close())
	buf.Write(gzipTrailer(0, 0))
	return &buf
}
