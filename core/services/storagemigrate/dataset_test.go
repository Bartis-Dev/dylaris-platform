package storagemigrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"sync"
	"testing"
)

// memDataSet is the in-memory DataSet used by every storagemigrate test in
// this package. It is defined ONCE, here, and later test files reuse it -
// never redeclare it.
//
// openErr / writeErr let a test inject a NON-not-found failure on a specific
// key, which is exactly the discrimination the copy loop depends on.
type memDataSet struct {
	mu       sync.Mutex
	id       string
	label    string
	objects  map[string][]byte
	openErr  map[string]error
	writeErr map[string]error
	// deleted records every Delete call, in order, so a test can prove the
	// source was never mutated.
	deleted []string
}

func newMemDataSet(id, label string) *memDataSet {
	return &memDataSet{
		id:       id,
		label:    label,
		objects:  map[string][]byte{},
		openErr:  map[string]error{},
		writeErr: map[string]error{},
	}
}

func (m *memDataSet) put(key, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = []byte(body)
}

func (m *memDataSet) snapshot() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.objects {
		out[k] = string(v)
	}
	return out
}

func (m *memDataSet) ID() string    { return m.id }
func (m *memDataSet) Label() string { return m.label }

func (m *memDataSet) List(_ context.Context) ([]ObjectRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ObjectRef, 0, len(m.objects))
	for k, v := range m.objects {
		out = append(out, ObjectRef{Key: k, Size: int64(len(v))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *memDataSet) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.openErr[key]; err != nil {
		return nil, err
	}
	b, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("memDataSet open %s: %w", key, fs.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), b...))), nil
}

func (m *memDataSet) Write(_ context.Context, key string, r io.Reader, _ int64) error {
	m.mu.Lock()
	if err := m.writeErr[key]; err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = b
	return nil
}

func (m *memDataSet) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, key)
	delete(m.objects, key)
	return nil
}

// memDataSet must satisfy the seam.
var _ DataSet = (*memDataSet)(nil)

func TestMemDataSet_SatisfiesTheSeamContract(t *testing.T) {
	ctx := context.Background()
	ds := newMemDataSet("library", "Library")
	if ds.ID() != "library" || ds.Label() != "Library" {
		t.Fatalf("ID/Label = %q/%q, want library/Library", ds.ID(), ds.Label())
	}

	if err := ds.Write(ctx, "a/b.txt", bytes.NewReader([]byte("hello")), 5); err != nil {
		t.Fatalf("Write: %v", err)
	}
	refs, err := ds.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(refs) != 1 || refs[0].Key != "a/b.txt" || refs[0].Size != 5 {
		t.Fatalf("List = %+v, want [{a/b.txt 5}]", refs)
	}

	rc, err := ds.Open(ctx, "a/b.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "hello" {
		t.Errorf("Open content = %q, want hello", got)
	}

	if err := ds.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Idempotent: a second Delete is not an error.
	if err := ds.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestMemDataSet_MissingKeyIsErrNotExistComparable(t *testing.T) {
	// The whole copy loop hangs on this: "missing" MUST be
	// errors.Is(err, fs.ErrNotExist) and everything else must not be.
	ds := newMemDataSet("library", "Library")
	_, err := ds.Open(context.Background(), "nope.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open(missing) err = %v, want errors.Is(err, fs.ErrNotExist)", err)
	}

	boom := errors.New("backend 503")
	ds.openErr["throttled.txt"] = boom
	ds.put("throttled.txt", "x")
	_, err = ds.Open(context.Background(), "throttled.txt")
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open(throttled) err = %v, must NOT look like fs.ErrNotExist", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("Open(throttled) err = %v, want it to wrap %v", err, boom)
	}
}
