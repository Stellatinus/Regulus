package backup

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// FormatVersion is the version of the file layout. A restore refuses a larger
// one: the file was made by a newer Regulus.
const FormatVersion = 1

// Names inside the archive.
const (
	manifestName = "manifest.json"
	secretName   = "secret.key"
	pkiPrefix    = "pki/"
	dbPrefix     = "db/"
	tableSuffix  = ".jsonl"
)

// ErrFormatNewer is the answer to a backup with a format this version does not know.
var ErrFormatNewer = errors.New("the backup was made by a newer version of Regulus")

// Part is a file of the archive with its size and SHA-256.
type Part struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes a backup. It is the first entry of the archive.
type Manifest struct {
	FormatVersion int       `json:"format_version"`
	HubVersion    string    `json:"hub_version"`
	SchemaVersion int       `json:"schema_version"`
	DatabaseType  string    `json:"database_type"`
	CreatedAt     time.Time `json:"created_at"`
	WithMetrics   bool      `json:"with_metrics"`
	// Tables are the tables of the export, in the order they are restored.
	Tables []string `json:"tables"`
	Parts  []Part   `json:"parts"`
}

func (m Manifest) part(name string) (Part, bool) {
	for _, p := range m.Parts {
		if p.Name == name {
			return p, true
		}
	}
	return Part{}, false
}

// header is the first line of a table file.
type header struct {
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
}

// encodeValue makes a database value fit JSON without losing its type.
func encodeValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return map[string]string{"$base64": base64.StdEncoding.EncodeToString(x)}
	case time.Time:
		return map[string]string{"$time": x.UTC().Format(time.RFC3339Nano)}
	}
	return v
}

// decodeValue is the reverse: numbers come back as int64 when they are whole
// numbers, as float64 otherwise.
func decodeValue(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		f, err := x.Float64()
		return f, err
	case map[string]any:
		if s, ok := x["$base64"].(string); ok {
			return base64.StdEncoding.DecodeString(s)
		}
		if s, ok := x["$time"].(string); ok {
			return time.Parse(time.RFC3339Nano, s)
		}
		return nil, fmt.Errorf("unknown value %v", x)
	}
	return v, nil // string, bool, nil
}

// rowReader reads the rows of one table file.
type rowReader struct {
	dec  *json.Decoder
	cols int
}

func newRowReader(r io.Reader, table string) (*rowReader, []string, error) {
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<16))
	dec.UseNumber()
	var h header
	if err := dec.Decode(&h); err != nil {
		return nil, nil, fmt.Errorf("table %s: %w", table, err)
	}
	if h.Table != table {
		return nil, nil, fmt.Errorf("the file of table %s says it is %q", table, h.Table)
	}
	return &rowReader{dec: dec, cols: len(h.Columns)}, h.Columns, nil
}

func (r *rowReader) next() ([]any, bool, error) {
	if !r.dec.More() {
		return nil, false, nil
	}
	var row []any
	if err := r.dec.Decode(&row); err != nil {
		return nil, false, err
	}
	if len(row) != r.cols {
		return nil, false, fmt.Errorf("a row has %d values, the table has %d columns", len(row), r.cols)
	}
	for i, v := range row {
		d, err := decodeValue(v)
		if err != nil {
			return nil, false, err
		}
		row[i] = d
	}
	return row, true, nil
}

// hashingWriter counts and hashes what passes through.
type hashingWriter struct {
	w io.Writer
	h interface {
		io.Writer
		Sum([]byte) []byte
	}
	n int64
}

func newHashingWriter(w io.Writer) *hashingWriter { return &hashingWriter{w: w, h: sha256.New()} }

func (h *hashingWriter) Write(p []byte) (int, error) {
	n, err := h.w.Write(p)
	h.h.Write(p[:n])
	h.n += int64(n)
	return n, err
}

func (h *hashingWriter) sum() string { return hex.EncodeToString(h.h.Sum(nil)) }

func jsonLine(w io.Writer, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(b.Bytes())
	return err
}
