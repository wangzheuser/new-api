package logger

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRetentionTestFile creates one deterministic log fixture.
func writeRetentionTestFile(t *testing.T, dir string, name string, content string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	require.NoError(t, os.Chtimes(path, modTime, modTime))
	return path
}

func TestCleanupLogFilesByDaysKeepsActiveAndUnmanagedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	oldTime := now.AddDate(0, 0, -10)
	recentTime := now.AddDate(0, 0, -2)
	activePath := writeRetentionTestFile(t, dir, "oneapi-20260801000000.log", "active", oldTime)
	deletedPath := writeRetentionTestFile(t, dir, "oneapi-20260802000000.log", "abc", oldTime)
	failedPath := writeRetentionTestFile(t, dir, "oneapi-20260803000000.log", "12345", oldTime)
	recentPath := writeRetentionTestFile(t, dir, "oneapi-20260817000000.log", "recent", recentTime)
	unmanagedPath := writeRetentionTestFile(t, dir, "application.log", "keep", oldTime)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "oneapi-directory.log"), 0o700))
	symlinkPath := filepath.Join(dir, "oneapi-symlink.log")
	symlinkCreated := os.Symlink(recentPath, symlinkPath) == nil

	result, err := cleanupLogFiles(
		dir,
		CleanupModeByDays,
		7,
		now,
		activePath,
		func(path string) error {
			if path == failedPath {
				return errors.New("fixture removal failure")
			}
			return os.Remove(path)
		},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, result.AttemptedCount)
	assert.Equal(t, 1, result.DeletedCount)
	assert.EqualValues(t, 3, result.FreedBytes)
	assert.Equal(t, []string{filepath.Base(failedPath)}, result.FailedFiles)
	_, err = os.Stat(deletedPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	keptPaths := []string{activePath, failedPath, recentPath, unmanagedPath}
	if symlinkCreated {
		keptPaths = append(keptPaths, symlinkPath)
	}
	for _, path := range keptPaths {
		_, statErr := os.Lstat(path)
		assert.NoError(t, statErr, path)
	}
}

func TestCleanupLogFilesByCountPreservesNewestFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeRetentionTestFile(t, dir, "oneapi-20260818030000.log", "newest", now)
	writeRetentionTestFile(t, dir, "oneapi-20260818020000.log", "second", now)
	thirdPath := writeRetentionTestFile(t, dir, "oneapi-20260818010000.log", "third", now)
	oldestPath := writeRetentionTestFile(t, dir, "oneapi-20260818000000.log", "oldest", now)

	result, err := cleanupLogFiles(dir, CleanupModeByCount, 2, now, "", os.Remove)
	require.NoError(t, err)
	assert.Equal(t, 2, result.AttemptedCount)
	assert.Equal(t, 2, result.DeletedCount)
	assert.EqualValues(t, len("third")+len("oldest"), result.FreedBytes)
	_, err = os.Stat(thirdPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(oldestPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	files, err := ListLogFiles(dir)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "oneapi-20260818030000.log", files[0].Name)
	assert.Equal(t, "oneapi-20260818020000.log", files[1].Name)
}

func TestRunLogRetentionDisabledLeavesFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := writeRetentionTestFile(t, dir, "oneapi-20200101000000.log", "keep", now.AddDate(-1, 0, 0))
	previousDir := *common.LogDir
	previousDays := common.GetServerLogRetentionDays()
	*common.LogDir = dir
	common.SetServerLogRetentionDays(0)
	t.Cleanup(func() {
		*common.LogDir = previousDir
		common.SetServerLogRetentionDays(previousDays)
	})

	runLogRetention(now)
	_, err := os.Stat(path)
	assert.NoError(t, err)
}

func TestCleanupLogFilesRejectsInvalidModeAndValue(t *testing.T) {
	_, err := CleanupLogFiles(t.TempDir(), "unknown", 1, time.Now())
	assert.Error(t, err)
	_, err = CleanupLogFiles(t.TempDir(), CleanupModeByDays, 0, time.Now())
	assert.Error(t, err)
}
