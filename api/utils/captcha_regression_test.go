package utils

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestCaptchaWrongAnswerConsumesChallenge(t *testing.T) {
	l := NewLoginLimiter(SecurityPolicy{CaptchaThreshold: 0})
	defer close(l.cleanupStop)
	l.RegisterProvider(&MockCaptchaProvider{})
	err, c := l.RequireCaptcha()
	if err != nil {
		t.Fatal(err)
	}
	if l.VerifyCaptcha(c.Id, "wrong") || l.VerifyCaptcha(c.Id, c.Answer) {
		t.Fatal("challenge survived failed attempt")
	}
	err, c = l.RequireCaptcha()
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.VerifyCaptcha(c.Id, c.Answer) {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent successes=%d", successes.Load())
	}
}
