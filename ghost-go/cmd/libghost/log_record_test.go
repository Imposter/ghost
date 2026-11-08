package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// textRecord returns what slog's TextHandler writes for one record.
func textRecord(t *testing.T, level slog.Level, msg string, args ...any) []byte {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.Level(-100)}))
	log.Log(context.Background(), level, msg, args...)
	return buf.Bytes()
}

func TestRecordLevel(t *testing.T) {
	for _, l := range []slog.Level{
		slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError,
		slog.LevelDebug + 2, slog.LevelError + 4, slog.LevelDebug - 3,
	} {
		require.Equal(t, l, recordLevel(textRecord(t, l, "m", "k", "v")), l.String())
	}
}

func TestRecordLevelIgnoresLevelInTheMessage(t *testing.T) {
	rec := textRecord(t, slog.LevelInfo, "peer said level=ERROR", "detail", " level=ERROR", "level", "ERROR")
	require.Contains(t, string(rec), "level=ERROR")
	require.Equal(t, slog.LevelInfo, recordLevel(rec))
}

func TestRecordLevelDefaultsToInfo(t *testing.T) {
	require.Equal(t, slog.LevelInfo, recordLevel(nil))
	require.Equal(t, slog.LevelInfo, recordLevel([]byte("plain text level=ERROR\n")))
	require.Equal(t, slog.LevelInfo, recordLevel([]byte("time=x level=LOUD msg=m\n")))
	require.Equal(t, slog.LevelWarn, recordLevel([]byte("level=WARN msg=m\n")))
}

func TestLogcatChunksShortRecordIsOneEntry(t *testing.T) {
	require.Equal(t, [][]byte{[]byte("a=1\nb=2")}, logcatChunks([]byte("a=1\nb=2\n"), 10))
	require.Equal(t, [][]byte{{}}, logcatChunks([]byte("\n"), 10))
}

func TestLogcatChunksSplitsALongRecordAtTheLimit(t *testing.T) {
	rec := strings.Repeat("x", 25) + "\n"
	chunks := logcatChunks([]byte(rec), 10)
	require.Equal(t, []string{strings.Repeat("x", 10), strings.Repeat("x", 10), "xxxxx"}, toStrings(chunks))
}

func TestLogcatChunksPrefersANewline(t *testing.T) {
	chunks := logcatChunks([]byte("aaaa\nbbbbbbbbbbbb\ncc"), 10)
	require.Equal(t, []string{"aaaa", "bbbbbbbbbb", "bb\ncc"}, toStrings(chunks))
}

func TestLogcatChunksKeepsUTF8Whole(t *testing.T) {
	rec := "abcdefgh" + strings.Repeat("é€😀", 20)
	chunks := logcatChunks([]byte(rec), 10)
	require.Equal(t, rec, strings.Join(toStrings(chunks), ""))
	for _, c := range chunks {
		require.LessOrEqual(t, len(c), 10)
		require.True(t, utf8.Valid(c), "%q", c)
	}
}

func TestLogcatChunksCarryAWholeLongRecord(t *testing.T) {
	big := strings.Repeat("0123456789abcdef", 1000)
	rec := textRecord(t, slog.LevelInfo, "long", "blob", big)
	chunks := logcatChunks(rec, logcatMaxEntry)
	require.Greater(t, len(chunks), 3)
	for _, c := range chunks {
		require.LessOrEqual(t, len(c), logcatMaxEntry)
	}
	require.Equal(t, string(bytes.TrimRight(rec, "\n")), strings.Join(toStrings(chunks), ""))
}

func toStrings(chunks [][]byte) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = string(c)
	}
	return out
}
