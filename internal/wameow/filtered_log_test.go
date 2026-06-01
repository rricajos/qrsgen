package wameow

import (
	"fmt"
	"strings"
	"testing"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// captureLogger guarda lo logueado para inspección en tests.
type captureLogger struct {
	lines []string
}

func (c *captureLogger) emit(level, msg string, args ...interface{}) {
	c.lines = append(c.lines, level+":"+fmt.Sprintf(msg, args...))
}

func (c *captureLogger) Warnf(msg string, args ...interface{})  { c.emit("WARN", msg, args...) }
func (c *captureLogger) Errorf(msg string, args ...interface{}) { c.emit("ERROR", msg, args...) }
func (c *captureLogger) Infof(msg string, args ...interface{})  { c.emit("INFO", msg, args...) }
func (c *captureLogger) Debugf(msg string, args ...interface{}) { c.emit("DEBUG", msg, args...) }
func (c *captureLogger) Sub(_ string) waLog.Logger              { return c }

func TestFilteredWALog_DropsKnownNoise(t *testing.T) {
	base := &captureLogger{}
	l := newFilteredWALog(base)

	l.Warnf("Failed to delete history sync media from server: status code 400")

	if len(base.lines) != 0 {
		t.Errorf("expected the noisy warn to be dropped, got %v", base.lines)
	}
}

func TestFilteredWALog_PassesThroughOtherLevels(t *testing.T) {
	base := &captureLogger{}
	l := newFilteredWALog(base)

	l.Infof("connected to whatsapp")
	l.Errorf("auth failed")
	l.Debugf("frame: %s", "abc")

	if len(base.lines) != 3 {
		t.Fatalf("expected 3 passthrough lines, got %d: %v", len(base.lines), base.lines)
	}
	if !strings.HasPrefix(base.lines[0], "INFO:") {
		t.Errorf("expected INFO first, got %q", base.lines[0])
	}
}

func TestFilteredWALog_PassesThroughNonMatchingWarns(t *testing.T) {
	base := &captureLogger{}
	l := newFilteredWALog(base)

	l.Warnf("something else went wrong: %d", 42)

	if len(base.lines) != 1 {
		t.Fatalf("expected the unrelated warn to pass through, got %v", base.lines)
	}
	if !strings.Contains(base.lines[0], "WARN:something else went wrong: 42") {
		t.Errorf("unexpected line: %q", base.lines[0])
	}
}

func TestFilteredWALog_SubReturnsFiltered(t *testing.T) {
	base := &captureLogger{}
	l := newFilteredWALog(base).Sub("client")

	// El logger devuelto por Sub también debe filtrar.
	l.Warnf("Failed to delete history sync media from server: meh")
	if len(base.lines) != 0 {
		t.Errorf("expected sub-logger to also filter, got %v", base.lines)
	}
}
