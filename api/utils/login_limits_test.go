package utils

import (
	"fmt"
	"testing"
	"time"
)

func TestSecurityStateCapacityFailsClosed(t *testing.T) {
	l := NewLoginLimiter(SecurityPolicy{CaptchaThreshold: 3})
	defer close(l.cleanupStop)
	l.RegisterProvider(&MockCaptchaProvider{})
	for i := 0; i < maxSecurityEntries; i++ {
		l.RecordFailedAttempt(fmt.Sprint(i))
		l.captchas[fmt.Sprint(i)] = CaptchaMeta{ExpiresAt: time.Now().Add(time.Minute)}
	}
	l.RecordFailedAttempt("overflow")
	if len(l.attempts) != maxSecurityEntries {
		t.Fatal("unbounded attempts map")
	}
	if banned, _ := l.CheckSecurityStatus("overflow"); !banned {
		t.Fatal("capacity failed open")
	}
	if err, _ := l.RequireCaptcha(); err == nil {
		t.Fatal("unbounded captcha map")
	}
	for i := 0; i < 2*maxAttemptsPerIP; i++ {
		l.RecordFailedAttempt("0")
	}
	if len(l.attempts["0"]) != maxAttemptsPerIP {
		t.Fatal("unbounded attempt history")
	}
}

func TestNegativeCaptchaThresholdDisablesOnlyCaptcha(t *testing.T) {
	l := NewLoginLimiter(SecurityPolicy{CaptchaThreshold: -1, BanThreshold: 2})
	defer close(l.cleanupStop)
	l.RecordFailedAttempt("ip")
	if banned, captcha := l.CheckSecurityStatus("ip"); banned || captcha {
		t.Fatal("disabled captcha required")
	}
	l.RecordFailedAttempt("ip")
	if banned, _ := l.CheckSecurityStatus("ip"); !banned {
		t.Fatal("ban disabled along with captcha")
	}
}
