// Package scip imports an existing SCIP index (a .scip protobuf file produced
// by an external indexer such as scip-java or scip-python) into codemap's
// normalized records: definitions, references, implementations and derivable
// file imports. SCIP occurrences carry no call-role evidence, so this package
// NEVER emits call edges — the call graph stays owned by the native and LSP
// backends (see docs/languages.md, "must not be relabeled as a call graph").
package scip

import (
	"bufio"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

// Index message field numbers from scip.proto's message Index.
const (
	fieldMetadata   = 1
	fieldDocument   = 2
	fieldSymbolInfo = 3
)

// ReadIndex decodes a length-delimited .scip stream (metadata, documents,
// external symbols — each prefixed with its field tag and byte length, the
// format every SCIP indexer writes) into a parsed index.
func ReadIndex(r io.Reader) (*pb.Index, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	idx := &pb.Index{}
	for {
		tag, err := binaryReadUvarint(br)
		if err == io.EOF {
			return idx, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read scip field tag: %w", err)
		}
		if tag&0x7 != 2 { // wire type must be length-delimited
			return nil, fmt.Errorf("read scip index: unexpected wire type %d", tag&0x7)
		}
		n, err := binaryReadUvarint(br)
		if err != nil {
			return nil, fmt.Errorf("read scip message length: %w", err)
		}
		if n > maxMessageBytes {
			return nil, fmt.Errorf("read scip index: message of %d bytes exceeds limit %d", n, maxMessageBytes)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			return nil, fmt.Errorf("read scip message body: %w", err)
		}
		switch tag >> 3 {
		case fieldMetadata:
			m := &pb.Metadata{}
			if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, m); err != nil {
				return nil, fmt.Errorf("parse scip metadata: %w", err)
			}
			idx.Metadata = m
		case fieldDocument:
			d := &pb.Document{}
			if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, d); err != nil {
				return nil, fmt.Errorf("parse scip document: %w", err)
			}
			idx.Documents = append(idx.Documents, d)
		case fieldSymbolInfo:
			s := &pb.SymbolInformation{}
			if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(body, s); err != nil {
				return nil, fmt.Errorf("parse scip external symbol: %w", err)
			}
			idx.ExternalSymbols = append(idx.ExternalSymbols, s)
		default:
			// Unknown field: skipped above via DiscardUnknown semantics at the
			// stream level (we simply don't decode it).
		}
	}
}

// maxMessageBytes caps one length-delimited protobuf message so a corrupt
// length prefix can't make the reader allocate unbounded memory.
const maxMessageBytes = 512 << 20

// WriteIndex encodes an index in the canonical length-delimited .scip format —
// the inverse of ReadIndex, used by tests to build hermetic fixtures without
// an external indexer.
func WriteIndex(w io.Writer, idx *pb.Index) error {
	bw := bufio.NewWriterSize(w, 1<<16)
	write := func(field uint64, m proto.Message) error {
		body, err := proto.Marshal(m)
		if err != nil {
			return err
		}
		writeUvarint(bw, field<<3|2)
		writeUvarint(bw, uint64(len(body)))
		if _, err := bw.Write(body); err != nil {
			return err
		}
		return nil
	}
	if idx.Metadata != nil {
		if err := write(fieldMetadata, idx.Metadata); err != nil {
			return err
		}
	}
	for _, d := range idx.Documents {
		if err := write(fieldDocument, d); err != nil {
			return err
		}
	}
	for _, s := range idx.ExternalSymbols {
		if err := write(fieldSymbolInfo, s); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeUvarint(bw *bufio.Writer, v uint64) {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	n++
	_, _ = bw.Write(buf[:n])
}

func binaryReadUvarint(br *bufio.Reader) (uint64, error) {
	v, err := br.ReadByte()
	if err != nil {
		return 0, err
	}
	x := uint64(v & 0x7f)
	for s := uint(7); v&0x80 != 0; s += 7 {
		if s > 63 {
			return 0, fmt.Errorf("invalid varint")
		}
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		v = b
		x |= uint64(v&0x7f) << s
	}
	return x, nil
}
