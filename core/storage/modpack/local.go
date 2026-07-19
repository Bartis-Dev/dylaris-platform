package modpack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LocalProvider writes each .mrpack to one or more local filesystem paths.
// Put fans out to all configured paths; Get/Stat probe paths in order and
// return the first hit; Delete is best-effort across all paths.
type LocalProvider struct {
	Paths []string
}

func (p *LocalProvider) ensureConfigured() error {
	if len(p.Paths) == 0 {
		return errors.New("modpack storage: no paths configured")
	}
	return nil
}

// Put writes data to every configured path. On partial failure (one or more
// writes succeeded before a later one failed), the already-written files are
// removed before the error is returned, so the user never observes a
// torn write across mirrors.
func (p *LocalProvider) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.ensureConfigured(); err != nil {
		return err
	}

	written := make([]string, 0, len(p.Paths))
	for _, base := range p.Paths {
		// Re-checked per mirror, not just on entry: each configured path can be a
		// separate mount, so a cancellation that arrives during the fan-out stops
		// the remaining ones instead of writing them all out regardless.
		if err := ctx.Err(); err != nil {
			for _, done := range written {
				_ = os.Remove(done)
			}
			return err
		}
		full := filepath.Join(base, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			// Rollback any successful writes.
			for _, done := range written {
				_ = os.Remove(done)
			}
			return fmt.Errorf("modpack storage: mkdir %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			for _, done := range written {
				_ = os.Remove(done)
			}
			return fmt.Errorf("modpack storage: write %s: %w", full, err)
		}
		written = append(written, full)
	}
	return nil
}

// Get returns the contents of the first path that holds the key. If every
// path reports os.ErrNotExist, ErrNotFound is returned. Any other error
// short-circuits the search.
func (p *LocalProvider) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.ensureConfigured(); err != nil {
		return nil, err
	}

	for _, base := range p.Paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		full := filepath.Join(base, filepath.FromSlash(key))
		data, err := os.ReadFile(full)
		if err == nil {
			return data, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return nil, fmt.Errorf("modpack storage: read %s: %w", full, err)
	}
	return nil, ErrNotFound
}

// Delete removes the key from every configured path. Missing files are not
// an error (idempotent). Only a non-ErrNotExist removal failure surfaces.
func (p *LocalProvider) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.ensureConfigured(); err != nil {
		return err
	}

	for _, base := range p.Paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		full := filepath.Join(base, filepath.FromSlash(key))
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("modpack storage: remove %s: %w", full, err)
		}
	}
	return nil
}

// Stat returns the size of the first path that contains the key.
// (0, false, nil) means no mirror holds it. Probe errors other than
// os.ErrNotExist short-circuit.
func (p *LocalProvider) Stat(ctx context.Context, key string) (int64, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if err := p.ensureConfigured(); err != nil {
		return 0, false, err
	}

	for _, base := range p.Paths {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		full := filepath.Join(base, filepath.FromSlash(key))
		info, err := os.Stat(full)
		if err == nil {
			return info.Size(), true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return 0, false, fmt.Errorf("modpack storage: stat %s: %w", full, err)
	}
	return 0, false, nil
}
