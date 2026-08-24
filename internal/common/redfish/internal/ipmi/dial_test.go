package ipmi

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestDialSOL_EchoSuite3(t *testing.T) {
	bmc, err := StartTestBMC(TestBMCOptions{Username: "admin", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	defer bmc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rw, err := DialSOL(ctx, Config{
		Host: "127.0.0.1", Port: bmc.Addr.Port,
		Username: "admin", Password: "password",
		Timeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("DialSOL: %v", err)
	}
	defer rw.Close()

	msg := []byte("hello-sol")
	if _, err := rw.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readN(t, rw, len(msg), 2*time.Second)
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo = %q, want %q", got, msg)
	}
}

func TestDialSOL_Suite17AfterSuite3Reject(t *testing.T) {
	bmc, err := StartTestBMC(TestBMCOptions{Username: "admin", Password: "password", RejectSuite3: true})
	if err != nil {
		t.Fatal(err)
	}
	defer bmc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rw, err := DialSOL(ctx, Config{
		Host: "127.0.0.1", Port: bmc.Addr.Port,
		Username: "admin", Password: "password",
		Timeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("DialSOL suite17: %v", err)
	}
	defer rw.Close()
	if _, err := rw.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readN(t, rw, 1, 2*time.Second)
	if string(got) != "x" {
		t.Fatalf("echo = %q", got)
	}
}

func TestDialSOL_UDPTimeoutNoSuite17(t *testing.T) {
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.LocalAddr().(*net.UDPAddr).Port

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = DialSOL(ctx, Config{
		Host: "127.0.0.1", Port: port,
		Username: "admin", Password: "password",
		Timeout: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if elapsed > 800*time.Millisecond {
		t.Fatalf("timeout took %s, want <800ms (must not shop cipher 17 after UDP timeout)", elapsed)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("udp 623 timeout")) {
		t.Fatalf("error = %q, want udp 623 timeout", err)
	}
}

func readN(t *testing.T, r io.Reader, n int, d time.Duration) []byte {
	t.Helper()
	done := make(chan struct {
		b []byte
		e error
	}, 1)
	go func() {
		got := make([]byte, 0, n)
		tmp := make([]byte, n)
		for len(got) < n {
			k, err := r.Read(tmp)
			if k > 0 {
				got = append(got, tmp[:k]...)
			}
			if err != nil {
				done <- struct {
					b []byte
					e error
				}{got, err}
				return
			}
		}
		done <- struct {
			b []byte
			e error
		}{got[:n], nil}
	}()
	select {
	case <-time.After(d):
		t.Fatalf("read timed out waiting for %d bytes", n)
		return nil
	case res := <-done:
		if res.e != nil && res.e != io.EOF && len(res.b) < n {
			t.Fatalf("read: %v", res.e)
		}
		return res.b
	}
}
