package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gilangmlr/claude-sync/internal/config"
	"github.com/gilangmlr/claude-sync/internal/crypto"
	"github.com/gilangmlr/claude-sync/internal/storage"
)

// mockStorage implements storage.Storage in-memory for testing.
type mockStorage struct {
	mu      sync.Mutex
	objects map[string]mockObject
}

type mockObject struct {
	data         []byte
	lastModified time.Time
}

func newMockStorage() *mockStorage {
	return &mockStorage{objects: make(map[string]mockObject)}
}

func (m *mockStorage) Upload(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.objects[key] = mockObject{data: cp, lastModified: time.Now()}
	return nil
}

func (m *mockStorage) Download(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	cp := make([]byte, len(obj.data))
	copy(cp, obj.data)
	return cp, nil
}

func (m *mockStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *mockStorage) DeleteBatch(_ context.Context, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.objects, k)
	}
	return nil
}

func (m *mockStorage) List(_ context.Context, prefix string) ([]storage.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []storage.ObjectInfo
	for key, obj := range m.objects {
		if strings.HasPrefix(key, prefix) {
			result = append(result, storage.ObjectInfo{
				Key:          key,
				Size:         int64(len(obj.data)),
				LastModified: obj.lastModified,
			})
		}
	}
	return result, nil
}

func (m *mockStorage) Head(_ context.Context, key string) (*storage.ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	return &storage.ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		LastModified: obj.lastModified,
	}, nil
}

func (m *mockStorage) BucketExists(_ context.Context) (bool, error) {
	return true, nil
}

// ListKeys returns all keys in the mock storage (for test assertions).
func (m *mockStorage) ListKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	return keys
}

// helper to create a test syncer with mock storage and temp dirs
type testEnv struct {
	syncer    *Syncer
	store     *mockStorage
	claudeDir string
	stateDir  string
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()
	tmpDir := t.TempDir()
	claudeDir := filepath.Join(tmpDir, ".claude")
	stateDir := filepath.Join(tmpDir, ".claude-sync")

	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create claude dir: %v", err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}

	// Generate encryption key
	keyPath := filepath.Join(stateDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "test-passphrase"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	store := newMockStorage()
	homeDir, _ := os.UserHomeDir()
	syncer := &Syncer{
		storage:    store,
		encryptor:  enc,
		state:      state,
		claudeDir:  claudeDir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper(homeDir),
	}

	return &testEnv{
		syncer:    syncer,
		store:     store,
		claudeDir: claudeDir,
		stateDir:  stateDir,
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s: %v", name, err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("Failed to read %s: %v", name, err)
	}
	return string(data)
}

func TestPushUploadsNewFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# My Settings")
	writeFile(t, env.claudeDir, "settings.json", `{"theme":"dark"}`)

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	if len(result.Uploaded) != 2 {
		t.Errorf("Expected 2 uploads, got %d: %v", len(result.Uploaded), result.Uploaded)
	}
	if len(result.Errors) > 0 {
		t.Errorf("Unexpected errors: %v", result.Errors)
	}

	// Verify files exist in mock storage with .age suffix
	objs, _ := env.store.List(ctx, "")
	if len(objs) != 2 {
		t.Errorf("Expected 2 objects in storage, got %d", len(objs))
	}
	for _, obj := range objs {
		if !strings.HasSuffix(obj.Key, ".age") {
			t.Errorf("Expected .age suffix on key %s", obj.Key)
		}
	}
}

func TestPushUploadsModifiedFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# V1")

	// Initial push
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Initial push failed: %v", err)
	}
	if len(result.Uploaded) != 1 {
		t.Fatalf("Expected 1 upload, got %d", len(result.Uploaded))
	}

	// Modify the file
	writeFile(t, env.claudeDir, "CLAUDE.md", "# V2 - modified")

	// Second push
	result, err = env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Second push failed: %v", err)
	}
	if len(result.Uploaded) != 1 {
		t.Errorf("Expected 1 modified upload, got %d", len(result.Uploaded))
	}
}

func TestPushDeletesRemovedFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# Settings")

	// Push
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Delete local file
	os.Remove(filepath.Join(env.claudeDir, "CLAUDE.md"))

	// Push again
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Delete push failed: %v", err)
	}
	if len(result.Deleted) != 1 {
		t.Errorf("Expected 1 delete, got %d", len(result.Deleted))
	}

	// Verify removed from storage
	objs, _ := env.store.List(ctx, "")
	if len(objs) != 0 {
		t.Errorf("Expected 0 objects in storage after delete, got %d", len(objs))
	}
}

func TestPullDownloadsNewRemoteFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Simulate remote files by encrypting and uploading directly to mock storage
	content := []byte("# Remote Settings")
	encrypted, err := env.syncer.encryptor.Encrypt(content)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if err := env.store.Upload(ctx, "CLAUDE.md.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 download, got %d: %v", len(result.Downloaded), result.Downloaded)
	}

	// Verify local file
	got := readFile(t, env.claudeDir, "CLAUDE.md")
	if got != "# Remote Settings" {
		t.Errorf("Expected '# Remote Settings', got %q", got)
	}
}

func TestPullSkipsUnchangedFiles(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# Synced")

	// Push to establish state
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Pull — nothing should be downloaded since remote hasn't changed beyond our push
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Downloaded) != 0 {
		t.Errorf("Expected 0 downloads (unchanged), got %d: %v", len(result.Downloaded), result.Downloaded)
	}
}

func TestPullMergesDivergedJSONL(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "history.jsonl", `{"ts":1,"event":"base"}`+"\n")
	// An unchanged file that should NOT be copied into the merge backup.
	writeFile(t, env.claudeDir, "CLAUDE.md", "# unchanged\n")

	// Push to establish baseline
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Local appends an entry.
	writeFile(t, env.claudeDir, "history.jsonl", `{"ts":1,"event":"base"}`+"\n"+`{"ts":2,"event":"local"}`+"\n")

	// Remote (another device) appends a different entry on top of the base.
	remoteContent := []byte(`{"ts":1,"event":"base"}` + "\n" + `{"ts":3,"event":"remote"}` + "\n")
	encrypted, err := env.syncer.encryptor.Encrypt(remoteContent)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "history.jsonl.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull — .jsonl should be union-merged, not sidecarred.
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("Expected 0 conflicts (jsonl merges), got %v", result.Conflicts)
	}
	if len(result.Merged) != 1 || result.Merged[0] != "history.jsonl" {
		t.Errorf("Expected history.jsonl in Merged, got %v", result.Merged)
	}

	// Merged local file contains all three entries (base + local + remote).
	got := readFile(t, env.claudeDir, "history.jsonl")
	for _, want := range []string{`"event":"base"`, `"event":"local"`, `"event":"remote"`} {
		if !strings.Contains(got, want) {
			t.Errorf("merged file missing %s; got %q", want, got)
		}
	}

	// No .conflict sidecar for a merged jsonl.
	entries, _ := os.ReadDir(env.claudeDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".conflict.") {
			t.Errorf("unexpected sidecar for merged jsonl: %s", e.Name())
		}
	}

	// A backup snapshot must have been created before the merge.
	if result.BackupDir == "" {
		t.Error("expected a backup dir before merge")
	}
	if _, err := os.Stat(filepath.Join(result.BackupDir, "history.jsonl")); err != nil {
		t.Errorf("backup should contain pre-merge history.jsonl: %v", err)
	}
	// Backup holds the pre-merge LOCAL content (not the union).
	if b := readFile(t, result.BackupDir, "history.jsonl"); strings.Contains(b, `"event":"remote"`) {
		t.Errorf("backup should be pre-merge local, got %q", b)
	}
	// Only merged files are backed up: the unchanged CLAUDE.md must be absent.
	if _, err := os.Stat(filepath.Join(result.BackupDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("unchanged CLAUDE.md should NOT be in the merge backup (err=%v)", err)
	}
}

func TestNoConflictWhenOnlyRemoteChanged(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "CLAUDE.md", "# V1")

	// Push to establish baseline
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Remote changes (another device pushes a new version)
	remoteContent := []byte("# V2 from other device")
	encrypted, err := env.syncer.encryptor.Encrypt(remoteContent)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "CLAUDE.md.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Local file NOT modified (hash matches state)
	// Pull — should download without conflict
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("Expected 0 conflicts, got %d", len(result.Conflicts))
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 download, got %d", len(result.Downloaded))
	}

	got := readFile(t, env.claudeDir, "CLAUDE.md")
	if got != "# V2 from other device" {
		t.Errorf("Expected remote content, got %q", got)
	}
}

func TestPushThenPullRoundTrip(t *testing.T) {
	// Device A pushes, Device B (fresh) pulls — content should match
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "shared-state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}

	// Shared encryption key
	keyPath := filepath.Join(stateDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "round-trip-test"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	sharedStore := newMockStorage()

	// Device A setup
	deviceADir := filepath.Join(tmpDir, "deviceA", ".claude")
	deviceAStateDir := filepath.Join(tmpDir, "deviceA", ".claude-sync")
	if err := os.MkdirAll(deviceADir, 0755); err != nil {
		t.Fatalf("Failed to create deviceA claude dir: %v", err)
	}
	if err := os.MkdirAll(deviceAStateDir, 0700); err != nil {
		t.Fatalf("Failed to create deviceA state dir: %v", err)
	}

	stateA, _ := LoadStateFromDir(deviceAStateDir)
	homeDir, _ := os.UserHomeDir()
	syncerA := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      stateA,
		claudeDir:  deviceADir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper(homeDir),
	}

	// Device A creates files and pushes
	writeFile(t, deviceADir, "CLAUDE.md", "# Shared config")
	writeFile(t, deviceADir, "settings.json", `{"theme":"dark","fontSize":14}`)
	writeFile(t, deviceADir, "agents/helper.json", `{"name":"helper","model":"opus"}`)

	ctx := context.Background()
	resultA, err := syncerA.Push(ctx)
	if err != nil {
		t.Fatalf("Device A push failed: %v", err)
	}
	if len(resultA.Uploaded) != 3 {
		t.Fatalf("Device A expected 3 uploads, got %d", len(resultA.Uploaded))
	}

	// Device B setup (fresh, no local files)
	deviceBDir := filepath.Join(tmpDir, "deviceB", ".claude")
	deviceBStateDir := filepath.Join(tmpDir, "deviceB", ".claude-sync")
	if err := os.MkdirAll(deviceBDir, 0755); err != nil {
		t.Fatalf("Failed to create deviceB claude dir: %v", err)
	}
	if err := os.MkdirAll(deviceBStateDir, 0700); err != nil {
		t.Fatalf("Failed to create deviceB state dir: %v", err)
	}

	stateB, _ := LoadStateFromDir(deviceBStateDir)
	syncerB := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      stateB,
		claudeDir:  deviceBDir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper(homeDir),
	}

	// Device B pulls
	resultB, err := syncerB.Pull(ctx)
	if err != nil {
		t.Fatalf("Device B pull failed: %v", err)
	}
	if len(resultB.Downloaded) != 3 {
		t.Errorf("Device B expected 3 downloads, got %d: %v", len(resultB.Downloaded), resultB.Downloaded)
	}

	// Verify content matches
	if got := readFile(t, deviceBDir, "CLAUDE.md"); got != "# Shared config" {
		t.Errorf("CLAUDE.md mismatch: %q", got)
	}
	if got := readFile(t, deviceBDir, "settings.json"); got != `{"theme":"dark","fontSize":14}` {
		t.Errorf("settings.json mismatch: %q", got)
	}
	if got := readFile(t, deviceBDir, "agents/helper.json"); got != `{"name":"helper","model":"opus"}` {
		t.Errorf("agents/helper.json mismatch: %q", got)
	}
}

func TestConflictCreatesConflictFile(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Structured JSON is NOT line-merged (would corrupt) -> sidecar path.
	writeFile(t, env.claudeDir, "settings.json", `{"theme":"base"}`)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Local change
	writeFile(t, env.claudeDir, "settings.json", `{"theme":"local"}`)

	// Remote also changed
	remoteData := []byte(`{"theme":"remote"}`)
	encrypted, _ := env.syncer.encryptor.Encrypt(remoteData)
	time.Sleep(10 * time.Millisecond)
	if err := env.store.Upload(ctx, "settings.json.age", encrypted); err != nil {
		t.Fatalf("Upload to mock failed: %v", err)
	}

	// Pull
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}

	// Should have conflict (not merge) for structured JSON.
	if len(result.Conflicts) != 1 || result.Conflicts[0] != "settings.json" {
		t.Fatalf("Expected 1 conflict on settings.json, got %v", result.Conflicts)
	}
	if len(result.Merged) != 0 {
		t.Errorf("structured JSON must not be merged, got %v", result.Merged)
	}

	// Local preserved
	if local := readFile(t, env.claudeDir, "settings.json"); local != `{"theme":"local"}` {
		t.Errorf("Local should be preserved, got %q", local)
	}

	// Conflict sidecar has remote content
	entries, _ := os.ReadDir(env.claudeDir)
	found := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "settings.json.conflict.") {
			found = true
			data, _ := os.ReadFile(filepath.Join(env.claudeDir, e.Name()))
			if string(data) != `{"theme":"remote"}` {
				t.Errorf("Conflict file content mismatch: %q", string(data))
			}
		}
	}
	if !found {
		t.Error("No .conflict file created")
	}
}

func TestFirstPullMergesExistingFilesInsteadOfOverwriting(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Local has content but has NEVER synced (no state) — the first-pull case.
	writeFile(t, env.claudeDir, "history.jsonl", `{"ts":1,"v":"local-only"}`+"\n")
	writeFile(t, env.claudeDir, "CLAUDE.md", "# notes\nlocal note\n")

	// Remote already has different content for both files.
	for key, content := range map[string]string{
		"history.jsonl.age": `{"ts":2,"v":"remote-only"}` + "\n",
		"CLAUDE.md.age":     "# notes\nremote note\n",
	} {
		enc, _ := env.syncer.encryptor.Encrypt([]byte(content))
		if err := env.store.Upload(ctx, key, enc); err != nil {
			t.Fatalf("upload %s: %v", key, err)
		}
	}

	if env.syncer.HasState() {
		t.Fatal("precondition: syncer should have no state")
	}

	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Merged) != 2 {
		t.Errorf("expected both files merged, got %v", result.Merged)
	}

	// .jsonl union: both entries survive (local not clobbered).
	hist := readFile(t, env.claudeDir, "history.jsonl")
	if !strings.Contains(hist, "local-only") || !strings.Contains(hist, "remote-only") {
		t.Errorf("history.jsonl should contain both, got %q", hist)
	}
	// markdown line-union: local note preserved + remote note appended.
	mem := readFile(t, env.claudeDir, "CLAUDE.md")
	if !strings.Contains(mem, "local note") || !strings.Contains(mem, "remote note") {
		t.Errorf("CLAUDE.md should contain both notes, got %q", mem)
	}

	// Backup taken before merging.
	if result.BackupDir == "" {
		t.Error("expected backup before first-pull merge")
	}
}

func TestPushNoChangesIsNoop(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Push with no files — should be a no-op
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if len(result.Uploaded) != 0 {
		t.Errorf("Expected 0 uploads, got %d", len(result.Uploaded))
	}
	if len(result.Deleted) != 0 {
		t.Errorf("Expected 0 deletes, got %d", len(result.Deleted))
	}
}

func TestPullEmptyRemoteIsNoop(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Pull with nothing in remote
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if len(result.Downloaded) != 0 {
		t.Errorf("Expected 0 downloads, got %d", len(result.Downloaded))
	}
}

func TestCrossDeviceProjectSync(t *testing.T) {
	// Simulates two machines with different home directories syncing project data.
	// Device A: home=/Users/merv → project dir -Users-merv-nexura
	// Device B: home=/Users/mervynlally → project dir -Users-mervynlally-nexura
	tmpDir := t.TempDir()

	keyPath := filepath.Join(tmpDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "cross-device-test"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	sharedStore := newMockStorage()
	ctx := context.Background()

	// --- Device A: /Users/merv ---
	deviceADir := filepath.Join(tmpDir, "deviceA", ".claude")
	deviceAStateDir := filepath.Join(tmpDir, "deviceA", ".claude-sync")
	os.MkdirAll(deviceADir, 0700)
	os.MkdirAll(deviceAStateDir, 0700)

	stateA, _ := LoadStateFromDir(deviceAStateDir)
	syncerA := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      stateA,
		claudeDir:  deviceADir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper("/Users/merv"),
	}

	// Create project files as they'd appear on Device A
	writeFile(t, deviceADir, "projects/-Users-merv-nexura/memory/MEMORY.md",
		"- [Repo info](repo.md) — origin is /Users/merv/nexura")
	writeFile(t, deviceADir, "projects/-Users-merv-nexura/memory/repo.md",
		"Local checkout at /Users/merv/nexura")
	writeFile(t, deviceADir, "projects/-Users-merv-nexura/abc123.jsonl",
		`{"type":"tool_result","path":"/Users/merv/nexura/src/app.ts"}`+"\n")
	// Also push a non-project file to verify it's unaffected
	writeFile(t, deviceADir, "CLAUDE.md", "# Global config")

	resultA, err := syncerA.Push(ctx)
	if err != nil {
		t.Fatalf("Device A push failed: %v", err)
	}
	if len(resultA.Uploaded) != 4 {
		t.Fatalf("Device A expected 4 uploads, got %d: %v", len(resultA.Uploaded), resultA.Uploaded)
	}

	// Verify remote keys use ${HOME} for project paths
	remoteKeys := sharedStore.ListKeys()
	for _, key := range remoteKeys {
		if strings.HasPrefix(key, "projects/") {
			if strings.Contains(key, "-Users-merv") {
				t.Errorf("Remote key should be normalized but contains literal home dir: %s", key)
			}
			if !strings.Contains(key, "${HOME}") {
				t.Errorf("Remote key should contain ${HOME}: %s", key)
			}
		}
	}

	// --- Device B: /Users/mervynlally ---
	deviceBDir := filepath.Join(tmpDir, "deviceB", ".claude")
	deviceBStateDir := filepath.Join(tmpDir, "deviceB", ".claude-sync")
	os.MkdirAll(deviceBDir, 0700)
	os.MkdirAll(deviceBStateDir, 0700)

	stateB, _ := LoadStateFromDir(deviceBStateDir)
	syncerB := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      stateB,
		claudeDir:  deviceBDir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper("/Users/mervynlally"),
	}

	resultB, err := syncerB.Pull(ctx)
	if err != nil {
		t.Fatalf("Device B pull failed: %v", err)
	}
	if len(resultB.Downloaded) != 4 {
		t.Fatalf("Device B expected 4 downloads, got %d: %v", len(resultB.Downloaded), resultB.Downloaded)
	}

	// Verify files landed under the CORRECT project dir for Device B
	memoryContent := readFile(t, deviceBDir, "projects/-Users-mervynlally-nexura/memory/MEMORY.md")
	if !strings.Contains(memoryContent, "/Users/mervynlally/nexura") {
		t.Errorf("Memory file should have resolved paths for Device B, got: %s", memoryContent)
	}
	if strings.Contains(memoryContent, "/Users/merv/") {
		t.Errorf("Memory file should NOT contain Device A paths, got: %s", memoryContent)
	}

	repoContent := readFile(t, deviceBDir, "projects/-Users-mervynlally-nexura/memory/repo.md")
	if !strings.Contains(repoContent, "/Users/mervynlally/nexura") {
		t.Errorf("Repo file should have resolved paths, got: %s", repoContent)
	}

	sessionContent := readFile(t, deviceBDir, "projects/-Users-mervynlally-nexura/abc123.jsonl")
	if !strings.Contains(sessionContent, "/Users/mervynlally/nexura/src/app.ts") {
		t.Errorf("Session file should have resolved paths, got: %s", sessionContent)
	}

	// Non-project file should be unaffected
	globalConfig := readFile(t, deviceBDir, "CLAUDE.md")
	if globalConfig != "# Global config" {
		t.Errorf("Global CLAUDE.md should be unchanged, got: %s", globalConfig)
	}
}

func TestCrossDeviceProjectSync_BothDirections(t *testing.T) {
	// Device A pushes, Device B pulls AND pushes new files, Device A pulls them back.
	tmpDir := t.TempDir()

	keyPath := filepath.Join(tmpDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "bidirectional-test"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	sharedStore := newMockStorage()
	ctx := context.Background()

	// Device A: /Users/merv
	deviceADir := filepath.Join(tmpDir, "deviceA", ".claude")
	deviceAStateDir := filepath.Join(tmpDir, "deviceA", ".claude-sync")
	os.MkdirAll(deviceADir, 0700)
	os.MkdirAll(deviceAStateDir, 0700)
	stateA, _ := LoadStateFromDir(deviceAStateDir)
	syncerA := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      stateA,
		claudeDir:  deviceADir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper("/Users/merv"),
	}

	// Device B: /Users/mervynlally
	deviceBDir := filepath.Join(tmpDir, "deviceB", ".claude")
	deviceBStateDir := filepath.Join(tmpDir, "deviceB", ".claude-sync")
	os.MkdirAll(deviceBDir, 0700)
	os.MkdirAll(deviceBStateDir, 0700)
	stateB, _ := LoadStateFromDir(deviceBStateDir)
	syncerB := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      stateB,
		claudeDir:  deviceBDir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper("/Users/mervynlally"),
	}

	// Step 1: Device A creates memory and pushes
	writeFile(t, deviceADir, "projects/-Users-merv-nexura/memory/MEMORY.md",
		"- [Auth info](auth.md) — session tokens in /Users/merv/nexura/config")
	if _, err := syncerA.Push(ctx); err != nil {
		t.Fatalf("Device A push failed: %v", err)
	}

	// Step 2: Device B pulls
	if _, err := syncerB.Pull(ctx); err != nil {
		t.Fatalf("Device B pull failed: %v", err)
	}

	// Verify Device B got the file with resolved paths
	got := readFile(t, deviceBDir, "projects/-Users-mervynlally-nexura/memory/MEMORY.md")
	if !strings.Contains(got, "/Users/mervynlally/nexura/config") {
		t.Fatalf("Device B should see resolved paths, got: %s", got)
	}

	// Step 3: Device B adds a new memory file and pushes
	writeFile(t, deviceBDir, "projects/-Users-mervynlally-nexura/memory/deploy.md",
		"Deploy script at /Users/mervynlally/nexura/scripts/deploy.sh")
	if _, err := syncerB.Push(ctx); err != nil {
		t.Fatalf("Device B push failed: %v", err)
	}

	// Step 4: Device A pulls
	if _, err := syncerA.Pull(ctx); err != nil {
		t.Fatalf("Device A pull failed: %v", err)
	}

	// Verify Device A got the new file with its own paths
	got = readFile(t, deviceADir, "projects/-Users-merv-nexura/memory/deploy.md")
	if !strings.Contains(got, "/Users/merv/nexura/scripts/deploy.sh") {
		t.Fatalf("Device A should see resolved paths, got: %s", got)
	}
	if strings.Contains(got, "/Users/mervynlally/") {
		t.Fatalf("Device A should NOT see Device B paths, got: %s", got)
	}
}

func TestCrossDeviceProjectSync_LegacyKeyPassthrough(t *testing.T) {
	// Simulates pulling data that was pushed by an older version of claude-sync
	// (before path normalization) — legacy keys without ${HOME} should still work.
	tmpDir := t.TempDir()

	keyPath := filepath.Join(tmpDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "legacy-test"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	sharedStore := newMockStorage()
	ctx := context.Background()

	// Simulate old-style push: upload with literal path in the remote key
	content := []byte("# Old memory content with /Users/merv/nexura path")
	compressed, _ := gzipCompress(content)
	encrypted, _ := enc.Encrypt(compressed)
	// Legacy key: no ${HOME}, literal home dir in path
	if err := sharedStore.Upload(ctx, "projects/-Users-merv-nexura/memory/old.md.age", encrypted); err != nil {
		t.Fatalf("Upload legacy key failed: %v", err)
	}

	// Pull on the SAME machine (legacy key matches local home dir)
	deviceDir := filepath.Join(tmpDir, "device", ".claude")
	deviceStateDir := filepath.Join(tmpDir, "device", ".claude-sync")
	os.MkdirAll(deviceDir, 0700)
	os.MkdirAll(deviceStateDir, 0700)
	state, _ := LoadStateFromDir(deviceStateDir)
	syncer := &Syncer{
		storage:    sharedStore,
		encryptor:  enc,
		state:      state,
		claudeDir:  deviceDir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper("/Users/merv"),
	}

	result, err := syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("Pull with legacy key failed: %v", err)
	}
	if len(result.Downloaded) != 1 {
		t.Fatalf("Expected 1 download, got %d", len(result.Downloaded))
	}

	// Legacy key resolves to same-machine local path (no ${HOME} to resolve)
	got := readFile(t, deviceDir, "projects/-Users-merv-nexura/memory/old.md")
	if !strings.Contains(got, "Old memory content") {
		t.Errorf("Legacy content should be preserved, got: %s", got)
	}
}

func TestMigrateRemoteKeys(t *testing.T) {
	tmpDir := t.TempDir()

	keyPath := filepath.Join(tmpDir, "age-key.txt")
	if err := crypto.GenerateKeyFromPassphrase(keyPath, "migrate-test"); err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	enc, err := crypto.NewEncryptor(keyPath)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	store := newMockStorage()
	ctx := context.Background()

	// Upload some legacy keys (literal home dir paths)
	for _, key := range []string{
		"projects/-Users-merv-nexura/memory/MEMORY.md.age",
		"projects/-Users-merv-nexura/abc.jsonl.age",
		"projects/-Users-mervynlally-nexura/memory/deploy.md.age",
	} {
		data, _ := enc.Encrypt([]byte("content for " + key))
		store.Upload(ctx, key, data)
	}

	// Upload a normalized key (should be skipped)
	normalized, _ := enc.Encrypt([]byte("already normalized"))
	store.Upload(ctx, "projects/${HOME}-nexura/memory/new.md.age", normalized)

	// Upload a non-project key (should be skipped)
	nonProject, _ := enc.Encrypt([]byte("settings"))
	store.Upload(ctx, "settings.json.age", nonProject)

	// Create syncer with /Users/merv as home dir
	deviceDir := filepath.Join(tmpDir, "device", ".claude")
	deviceStateDir := filepath.Join(tmpDir, "device", ".claude-sync")
	os.MkdirAll(deviceDir, 0700)
	os.MkdirAll(deviceStateDir, 0700)
	state, _ := LoadStateFromDir(deviceStateDir)
	syncer := &Syncer{
		storage:    store,
		encryptor:  enc,
		state:      state,
		claudeDir:  deviceDir,
		quiet:      true,
		cfg:        &config.Config{},
		pathMapper: NewPathMapper("/Users/merv"),
	}

	// Dry run first
	dryResult, err := syncer.MigrateRemoteKeys(ctx, true)
	if err != nil {
		t.Fatalf("Dry run failed: %v", err)
	}
	// Should identify the 2 keys owned by /Users/merv as migratable
	// The -Users-mervynlally key can't be normalized by this machine
	if len(dryResult.Migrated) != 2 {
		t.Errorf("Dry run: expected 2 migratable, got %d: %v", len(dryResult.Migrated), dryResult.Migrated)
	}
	// Verify nothing actually changed
	allKeys := store.ListKeys()
	if len(allKeys) != 5 {
		t.Errorf("Dry run should not change storage: expected 5 keys, got %d", len(allKeys))
	}

	// Real migration
	result, err := syncer.MigrateRemoteKeys(ctx, false)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	if len(result.Errors) > 0 {
		t.Errorf("Unexpected errors: %v", result.Errors)
	}
	// 2 legacy keys from /Users/merv migrated
	if len(result.Migrated) != 2 {
		t.Errorf("Expected 2 migrated, got %d: %v", len(result.Migrated), result.Migrated)
	}

	// Verify old keys are gone and normalized keys exist
	finalKeys := store.ListKeys()
	for _, key := range finalKeys {
		if strings.Contains(key, "-Users-merv-") && strings.HasPrefix(key, "projects/") {
			t.Errorf("Legacy key should be deleted: %s", key)
		}
	}
	// Should have: 2 normalized merv keys, 1 mervynlally legacy (can't migrate), 1 already-normalized, 1 settings
	if len(finalKeys) != 5 {
		t.Errorf("Expected 5 final keys, got %d: %v", len(finalKeys), finalKeys)
	}
}
