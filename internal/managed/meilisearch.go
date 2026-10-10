package managed

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abandon1a2b/ohyeah/internal/config"
	"github.com/gofrs/flock"
)

const Version = "v1.54.3"

type asset struct{ name, checksum, id string }

// Community edition asset IDs and SHA-256 digests from the pinned GitHub release.
var assets = map[string]asset{
	"darwin/arm64":  {"meilisearch-macos-apple-silicon", "2ce65b8f8e74eee14b8eef8a9d0fc125cdcc9a0227bbba89dca832a93505c714", "602976952"},
	"darwin/amd64":  {"meilisearch-macos-amd64", "82126065011d4b2b4d60abbcf467beeb72bd04f0824a24991dfb67bbaaed4e61", "603006305"},
	"linux/arm64":   {"meilisearch-linux-aarch64", "09ce5f9531bb205a10fe57854f64a4b59d16166a03de7fa76392a629ccfe81c4", "602956619"},
	"linux/amd64":   {"meilisearch-linux-amd64", "0ece934f9791f0db83e1184f4f505a38eae7d93ff1c47efaa9426cf2b3a12ba7", "602961009"},
	"windows/amd64": {"meilisearch-windows-amd64.exe", "09266bd2c51747cebe908f7373cc6e2e546e3930886bb8c1427f402a0dc4c2dc", "602965698"},
}

// Resolve shares the managed instance's persisted credentials with local commands.
func Resolve(cfg *config.MeilisearchConfig) error {
	if cfg.Mode != "managed" {
		return nil
	}
	if _, err := cfg.ManagedAddress(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Directory, 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(cfg.Directory, "credentials.lock"))
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Unlock()
	path := filepath.Join(cfg.Directory, "master-key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if cfg.APIKey == "" {
			bytes := make([]byte, 32)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			cfg.APIKey = hex.EncodeToString(bytes)
		}
		key = []byte(cfg.APIKey)
		if len(key) < 16 {
			return errors.New("managed Meilisearch api_key must contain at least 16 bytes")
		}
		if err := writeKey(path, key); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if len(key) < 16 {
		return errors.New("managed Meilisearch master-key is invalid")
	}
	if cfg.APIKey != "" && cfg.APIKey != string(key) {
		return errors.New("managed Meilisearch api_key differs from persisted master-key")
	}
	cfg.APIKey = string(key)
	return nil
}

func writeKey(path string, key []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".master-key-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(key); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

type Process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	lock *flock.Flock
	log  *os.File
}

func Start(ctx context.Context, cfg config.MeilisearchConfig) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	address, err := cfg.ManagedAddress()
	if err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(cfg.Directory, "serve.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("managed Meilisearch is already owned by another serve process")
	}
	defer func() {
		if err != nil {
			_ = lock.Unlock()
		}
	}()
	probe, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("managed Meilisearch address %s is occupied; configure external mode to use an existing instance: %w", address, err)
	}
	_ = probe.Close()
	binary, err := install(ctx, cfg.Directory)
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(cfg.Directory, "meilisearch.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "--http-addr", address, "--db-path", filepath.Join(cfg.Directory, "data-"+Version), "--env", "production", "--no-analytics")
	cmd.Dir = cfg.Directory
	// Do not inherit unrelated MEILI_* overrides from the parent environment.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "MEILI_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "MEILI_MASTER_KEY="+cfg.APIKey)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	p := &Process{cmd: cmd, done: make(chan struct{}), lock: lock, log: logFile}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := &http.Client{Timeout: time.Second}
	for {
		select {
		case <-p.done:
			err = fmt.Errorf("Meilisearch exited during startup: %v; see %s", p.err, logFile.Name())
			_ = p.Close()
			return nil, err
		case <-readyCtx.Done():
			err = fmt.Errorf("Meilisearch startup: %w; see %s", readyCtx.Err(), logFile.Name())
			_ = p.Close()
			return nil, err
		default:
		}
		req, _ := http.NewRequestWithContext(readyCtx, http.MethodGet, strings.TrimRight(cfg.URL, "/")+"/health", nil)
		resp, requestErr := client.Do(req)
		if requestErr == nil {
			var health struct {
				Status string `json:"status"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decodeErr == nil && health.Status == "available" {
				return p, nil
			}
		}
		select {
		case <-readyCtx.Done():
		case <-p.done:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (p *Process) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return fmt.Errorf("managed Meilisearch exited: %v; see %s", p.err, p.log.Name())
	}
}

func (p *Process) Close() error {
	select {
	case <-p.done:
	default:
		if runtime.GOOS == "windows" {
			_ = p.cmd.Process.Kill()
		} else {
			_ = p.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	}
	_ = p.log.Close()
	return p.lock.Unlock()
}

func install(ctx context.Context, directory string) (string, error) {
	a, ok := assets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("managed Meilisearch does not support %s/%s; use external mode", runtime.GOOS, runtime.GOARCH)
	}
	path := filepath.Join(directory, Version+"-"+a.name)
	if err := verify(path, a.checksum); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	slog.Info("downloading Meilisearch", "version", Version, "asset", a.name)
	downloadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	uri := "https://api.github.com/repos/meilisearch/meilisearch/releases/assets/" + a.id
	if err := download(downloadCtx, http.DefaultClient, uri, path, a.checksum); err != nil {
		return "", fmt.Errorf("install Meilisearch: %w", err)
	}
	return path, nil
}

func verify(path, checksum string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != checksum {
		return fmt.Errorf("Meilisearch checksum mismatch: %s", path)
	}
	return nil
}

func download(ctx context.Context, client *http.Client, uri, path, checksum string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".meilisearch-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	for attempt := 0; attempt < 3; attempt++ {
		err = downloadPart(ctx, client, uri, file)
		if err == nil {
			break
		}
		if attempt == 2 || ctx.Err() != nil {
			return err
		}
		slog.Warn("Meilisearch download interrupted; retrying", "attempt", attempt+1, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := verify(file.Name(), checksum); err != nil {
		return err
	}
	if err := os.Chmod(file.Name(), 0700); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func downloadPart(ctx context.Context, client *http.Client, uri string, file *os.File) error {
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "ohyeah-managed-meilisearch")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		// A server may ignore Range; restart rather than append duplicate bytes.
		if err := file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		offset = 0
	case http.StatusPartialContent:
		var start, end, total int64
		if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != offset || end != total-1 || total > 512<<20 {
			return errors.New("invalid Meilisearch download Content-Range")
		}
	default:
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	if _, err := io.Copy(file, io.LimitReader(resp.Body, (512<<20)+1-offset)); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > 512<<20 {
		return errors.New("Meilisearch download exceeds 512 MiB")
	}
	return nil
}
