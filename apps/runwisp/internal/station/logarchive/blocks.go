// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logarchive

import (
	"bufio"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
)

// An archive is one gzip member (browsers' DecompressionStream rejects more)
// whose deflate stream restarts every blockMinRaw raw bytes, at the end of a
// line: each block is deflated by a fresh compressor, and every block but the
// last ends with a sync flush, so it starts byte-aligned, refers to nothing
// before it and inflates on its own from its offset. A reader reads a block's
// raw length from it; past that the stream runs into the next block (or, cut
// at its compressed length, ends without a final block).
//
// The block table rides in the gzip header's FEXTRA field as subfield "RB",
// which gunzip and other readers skip: byte rbVersion, then per block
// uvarint(compressed length) and uvarint(raw length). Compressed lengths
// count from the first deflate byte, right after the header. An archive with
// too many blocks for FEXTRA's 64 KiB keeps the layout and drops the table.
const (
	blockMinRaw = 4 << 20
	rbVersion   = 1
)

// archiveBlock is one entry of the RB table.
type archiveBlock struct {
	compressed, raw int64
}

// deflateBlocks deflates src into dst block by block and returns the block
// table, the CRC-32 of src and its length.
func deflateBlocks(dst io.Writer, src io.Reader) ([]archiveBlock, uint32, int64, error) {
	buffered := bufio.NewWriter(dst)
	out := &countingWriter{w: buffered}
	in := bufio.NewReader(src)
	crc := crc32.NewIEEE()
	var blocks []archiveBlock
	var total int64
	for last := false; !last; {
		fw, err := flate.NewWriter(out, flate.DefaultCompression)
		if err != nil {
			return nil, 0, 0, err
		}
		start := out.n
		raw, eof, err := copyBlock(io.MultiWriter(fw, crc), in)
		if err != nil {
			return nil, 0, 0, err
		}
		last = eof
		if last {
			err = fw.Close() // the final deflate block
		} else {
			err = fw.Flush()
		}
		if err != nil {
			return nil, 0, 0, err
		}
		blocks = append(blocks, archiveBlock{compressed: out.n - start, raw: raw})
		total += raw
	}
	return blocks, crc.Sum32(), total, buffered.Flush()
}

// copyBlock copies one block's raw bytes: blockMinRaw, then on to the end of
// that line. eof reports nothing follows it.
func copyBlock(dst io.Writer, in *bufio.Reader) (n int64, eof bool, err error) {
	n, err = io.CopyN(dst, in, blockMinRaw)
	if errors.Is(err, io.EOF) {
		return n, true, nil
	}
	if err != nil {
		return n, false, err
	}
	for {
		line, readErr := in.ReadSlice('\n')
		written, err := dst.Write(line)
		n += int64(written)
		switch {
		case err != nil:
			return n, false, err
		case readErr == nil:
			_, peekErr := in.Peek(1)
			return n, errors.Is(peekErr, io.EOF), nil
		case errors.Is(readErr, io.EOF):
			return n, true, nil
		case !errors.Is(readErr, bufio.ErrBufferFull):
			return n, false, readErr
		}
	}
}

// gzipHeader is a gzip member header (no name, comment, mtime or header CRC)
// carrying the RB block table when it fits.
func gzipHeader(blocks []archiveBlock) []byte {
	header := []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 255} // ID1 ID2 CM=deflate FLG MTIME XFL OS=unknown
	table := []byte{rbVersion}
	for _, b := range blocks {
		table = binary.AppendUvarint(table, uint64(b.compressed))
		table = binary.AppendUvarint(table, uint64(b.raw))
	}
	const subfieldHeader = 4 // SI1 SI2 LEN
	if subfieldHeader+len(table) > math.MaxUint16 {
		return header
	}
	header[3] = 1 << 2 // FLG.FEXTRA
	header = binary.LittleEndian.AppendUint16(header, uint16(subfieldHeader+len(table)))
	header = append(header, 'R', 'B')
	header = binary.LittleEndian.AppendUint16(header, uint16(len(table)))
	return append(header, table...)
}

// gzipTrailer is a gzip member trailer: CRC-32 and size mod 2^32.
func gzipTrailer(crc uint32, size int64) []byte {
	trailer := binary.LittleEndian.AppendUint32(nil, crc)
	return binary.LittleEndian.AppendUint32(trailer, uint32(size)) // ISIZE is the size mod 2^32
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
