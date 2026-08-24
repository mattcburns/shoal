package ipmi

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

type solConn struct {
	s        *sess
	cancel   context.CancelFunc
	pumpDone chan struct{}

	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	rerr   error
	closed bool
}

func newSOL(parent context.Context, s *sess) *solConn {
	ctx, cancel := context.WithCancel(parent)
	c := &solConn{s: s, cancel: cancel, pumpDone: make(chan struct{})}
	c.cond = sync.NewCond(&c.mu)
	go c.pump(ctx)
	go c.keepalive(ctx)
	return c
}

func (c *solConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.buf) == 0 && c.rerr == nil && !c.closed {
		c.cond.Wait()
	}
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	if c.rerr != nil {
		return 0, c.rerr
	}
	return 0, io.EOF
}

func (c *solConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, io.ErrClosedPipe
	}
	c.s.solSeq++
	if c.s.solSeq == 0 || c.s.solSeq > 15 {
		c.s.solSeq = 1
	}
	if err := c.s.sendSOL(context.Background(), c.s.solSeq, c.s.lastBMCSeq, c.s.lastAccepted, 0, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *solConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	c.cancel()
	_ = c.s.conn.SetReadDeadline(time.Now())
	select {
	case <-c.pumpDone:
	case <-time.After(time.Second):
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.s.deactivate(ctx)
	return c.s.conn.Close()
}

func (c *solConn) pump(ctx context.Context) {
	defer close(c.pumpDone)
	defer c.fail(io.EOF)
	for {
		if ctx.Err() != nil {
			return
		}
		raw, err := c.s.readUDP(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			c.fail(err)
			return
		}
		plus, err := parseRMCPPlus(raw, &c.s.keys)
		if err != nil {
			continue
		}
		switch plus.payloadType & 0x3F {
		case payloadSOL:
			c.handleSOL(ctx, plus.payload)
		default:
			// ignore late IPMI
		}
	}
}

func (c *solConn) handleSOL(ctx context.Context, body []byte) {
	if len(body) < 4 {
		return
	}
	seq := body[0] & 0x0F
	op := body[3]
	if op&0x10 != 0 {
		c.fail(fmt.Errorf("ipmi: sol deactivating"))
		return
	}
	chars := body[4:]
	if seq != 0 && len(chars) > 0 {
		c.mu.Lock()
		c.buf = append(c.buf, chars...)
		c.s.lastBMCSeq = seq
		c.s.lastAccepted = byte(len(chars))
		c.cond.Signal()
		c.mu.Unlock()
		_ = c.s.sendSOL(ctx, 0, seq, byte(len(chars)), 0, nil)
	}
}

func (c *solConn) keepalive(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.mu.Lock()
			ack, n := c.s.lastBMCSeq, c.s.lastAccepted
			closed := c.closed
			c.mu.Unlock()
			if closed {
				return
			}
			_ = c.s.sendSOL(ctx, 0, ack, n, 0, nil)
		}
	}
}

func (c *solConn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rerr == nil {
		c.rerr = err
	}
	c.cond.Broadcast()
}
