package main

import (
	"bytes"
	"log/slog"
	"unicode/utf8"
)

// logcatMaxEntry is the most bytes one logcat entry carries: logcat truncates a
// message near 4 KiB (LOGGER_ENTRY_MAX_PAYLOAD less the header and tag), so a
// longer record goes as several entries of at most this many bytes.
const logcatMaxEntry = 4000

// recordLevel returns the level of a record slog's TextHandler wrote: the
// level= field that follows time= at its start. A record without one, or with
// a level slog cannot parse, is at Info. Text later in the record, such as a
// message that says "level=ERROR", is not a level.
func recordLevel(record []byte) slog.Level {
	rest := record
	if after, ok := bytes.CutPrefix(rest, []byte("time=")); ok {
		_, rest, _ = bytes.Cut(after, []byte(" "))
	}
	value, ok := bytes.CutPrefix(rest, []byte("level="))
	if !ok {
		return slog.LevelInfo
	}
	if end := bytes.IndexAny(value, " \n"); end >= 0 {
		value = value[:end]
	}
	var l slog.Level
	if err := l.UnmarshalText(value); err != nil {
		return slog.LevelInfo
	}
	return l
}

// logcatChunks splits a record, less its trailing newline, into consecutive
// pieces of at most limit bytes each, so logcat keeps all of it. A piece ends
// at the last newline that fits (the newline itself is dropped), else at the
// limit, moved back to the start of a UTF-8 sequence so no character is split.
// A record that fits is one piece, newlines and all.
func logcatChunks(record []byte, limit int) [][]byte {
	record = bytes.TrimRight(record, "\n")
	var chunks [][]byte
	for len(record) > limit {
		if nl := bytes.LastIndexByte(record[:limit+1], '\n'); nl > 0 {
			chunks = append(chunks, record[:nl])
			record = record[nl+1:]
			continue
		}
		cut := limit
		for cut > 0 && !utf8.RuneStart(record[cut]) {
			cut--
		}
		if cut == 0 {
			cut = limit
		}
		chunks = append(chunks, record[:cut])
		record = record[cut:]
	}
	return append(chunks, record)
}
