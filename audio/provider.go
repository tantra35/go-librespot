package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
)

type KeyProviderError struct {
	Code uint16
}

func (e KeyProviderError) Error() string {
	return fmt.Sprintf("failed retrieving aes key with code %d", e.Code)
}

type KeyProvider struct {
	ap  *ap.Accesspoint
	log librespot.Logger

	recvLoopOnce sync.Once

	reqChan chan *keyRequest
	stopCh  chan struct{}

	reqs     map[uint32]*keyRequest
	seq      uint32
	reqsLock sync.RWMutex
}

type keyRequest struct {
	gid    []byte
	fileId []byte
	seq    uint32
	ctx    context.Context
	resp   chan keyResponse
}

type keyResponse struct {
	key []byte
	err error
}

func NewAudioKeyProvider(log librespot.Logger, ap *ap.Accesspoint) *KeyProvider {
	p := &KeyProvider{log: log, ap: ap, reqs: make(map[uint32]*keyRequest)}
	p.reqChan = make(chan *keyRequest)
	p.stopCh = make(chan struct{})
	return p
}

func (p *KeyProvider) startReceiving() {
	p.recvLoopOnce.Do(func() { go p.recvLoop() })
}

func (p *KeyProvider) recvLoop() {
	ch := p.ap.Receive(ap.PacketTypeAesKey, ap.PacketTypeAesKeyError)

	for {
		select {
		case <-p.stopCh:
			return
		case pkt := <-ch:
			resp := bytes.NewReader(pkt.Payload)
			var respSeq uint32
			_ = binary.Read(resp, binary.BigEndian, &respSeq)

			p.reqsLock.RLock()
			req, ok := p.reqs[respSeq]
			p.reqsLock.RUnlock()
			if !ok {
				p.log.Warnf("received aes key with invalid sequence: %d", respSeq)
				continue
			}

			p.reqsLock.Lock()
			delete(p.reqs, respSeq)
			p.reqsLock.Unlock()

			switch pkt.Type {
			case ap.PacketTypeAesKey:
				key := make([]byte, 16)
				_, _ = resp.Read(key)
				req.resp <- keyResponse{key: key}
				p.log.Debugf("received aes key for file %s, gid: %s", hex.EncodeToString(req.fileId), librespot.GidToBase62(req.gid))

			case ap.PacketTypeAesKeyError:
				var errCode uint16
				_ = binary.Read(resp, binary.BigEndian, &errCode)
				req.resp <- keyResponse{err: &KeyProviderError{errCode}}
			default:
				panic("unexpected packet type")
			}
		case req := <-p.reqChan:
			var buf bytes.Buffer
			_, _ = buf.Write(req.fileId)
			_, _ = buf.Write(req.gid)
			_ = binary.Write(&buf, binary.BigEndian, req.seq)
			_ = binary.Write(&buf, binary.BigEndian, uint16(0))

			if err := p.ap.Send(req.ctx, ap.PacketTypeRequestKey, buf.Bytes()); err != nil {
				p.reqsLock.Lock()
				delete(p.reqs, req.seq)
				p.reqsLock.Unlock()
				req.resp <- keyResponse{err: fmt.Errorf("failed sending key request for file %s, gid: %s: %w",
					hex.EncodeToString(req.fileId), librespot.GidToBase62(req.gid), err)}

				p.log.Errorf("Can't request aes key for file %s, gid: %s, due: %s", hex.EncodeToString(req.fileId), librespot.GidToBase62(req.gid), err)
				continue
			}

			p.log.Debugf("requested aes key for file %s, gid: %s", hex.EncodeToString(req.fileId), librespot.GidToBase62(req.gid))
		}
	}
}

func (p *KeyProvider) Request(ctx context.Context, gid []byte, fileId []byte) ([]byte, error) {
	p.startReceiving()

	p.reqsLock.Lock()
	reqSeq := p.seq
	p.seq++
	req := &keyRequest{gid: gid, fileId: fileId, seq: reqSeq, ctx: ctx, resp: make(chan keyResponse, 1)}
	p.reqs[reqSeq] = req
	p.reqsLock.Unlock()

	select {
	case p.reqChan <- req:
	case <-p.stopCh:
		return nil, context.Canceled
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		p.reqsLock.Lock()
		delete(p.reqs, reqSeq)
		p.reqsLock.Unlock()
		return nil, ctx.Err()
	case resp := <-req.resp:
		if resp.err != nil {
			return nil, resp.err
		}

		return resp.key, nil
	}
}

func (p *KeyProvider) Close() {
	close(p.stopCh)
}
