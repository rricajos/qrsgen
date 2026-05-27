package errcode

import (
	"strings"
	"testing"
)

func TestCodesFollowPattern(t *testing.T) {
	codes := []string{SpamguardBlocked, HMACMismatch, PayloadInvalid, QueueFull, InstanceNotFound, TenantNotFound, Internal}
	for _, c := range codes {
		if !strings.HasPrefix(c, "QRSGEN_") {
			t.Errorf("code %q should start with QRSGEN_", c)
		}
		if c != strings.ToUpper(c) {
			t.Errorf("code %q should be UPPERCASE", c)
		}
	}
}

func TestHumanTextReturnsString(t *testing.T) {
	if HumanText(SpamguardBlocked) == "" {
		t.Error("spamguard text empty")
	}
	if HumanText("QRSGEN_UNKNOWN") != "QRSGEN_UNKNOWN" {
		t.Error("unknown code should echo back")
	}
}
