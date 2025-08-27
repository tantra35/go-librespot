package dealer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/coder/websocket"
	librespot "github.com/devgianlu/go-librespot"
	log "github.com/sirupsen/logrus"
)

const (
	pingInterval = 30 * time.Second
	timeout      = 10 * time.Second
)

type Dealer struct {
	log librespot.Logger

	client *http.Client

	addr        librespot.GetAddressFunc
	accessToken librespot.GetLogin5TokenFunc

	conn *websocket.Conn

	stop         bool
	stopCh       chan struct{}
	recvLoopOnce sync.Once
	lastPong     time.Time
	lastPongLock sync.Mutex

	// lock is held for writing when performing reconnection and for reading when accessing the conn.
	// If it's not held, a valid connection is available. Be careful not to deadlock anything with this.
	lock sync.RWMutex

	messageReceivers     []messageReceiver
	messageReceiversLock sync.RWMutex

	requestReceivers     map[string]requestReceiver
	requestReceiversLock sync.RWMutex
}

func NewDealer(log librespot.Logger, client *http.Client, dealerAddr librespot.GetAddressFunc, accessToken librespot.GetLogin5TokenFunc) *Dealer {
	return &Dealer{
		client: &http.Client{
			Transport:     client.Transport,
			CheckRedirect: client.CheckRedirect,
			Jar:           client.Jar,
			Timeout:       timeout,
		},
		log:              log,
		addr:             dealerAddr,
		accessToken:      accessToken,
		requestReceivers: map[string]requestReceiver{},
	}
}

func (d *Dealer) Connect(ctx context.Context) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	if d.conn != nil && !d.stop {
		d.log.Debugf("dealer connection already opened")
		return nil
	}

	d.stop = false
	d.stopCh = make(chan struct{})

	return d.connect(ctx)
}

func (d *Dealer) connect(ctx context.Context) error {
	accessToken, err := d.accessToken(ctx, false)
	if err != nil {
		return fmt.Errorf("failed obtaining dealer access token: %w", err)
	}

	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("wss://%s/?access_token=%s", d.addr(ctx), accessToken), &websocket.DialOptions{
		HTTPClient: d.client,
		HTTPHeader: http.Header{
			"User-Agent": []string{librespot.UserAgent()},
		},
	})
	if err != nil {
		return err
	}

	// we assign to d.conn after because if Dial fails we'll have a nil d.conn which we don't want
	d.conn = conn

	// remove the read limit
	d.conn.SetReadLimit(math.MaxUint32)

	d.log.Debugf("dealer connection opened")

	return nil
}

func (d *Dealer) Close() {
	d.lock.Lock()
	defer d.lock.Unlock()

	if !d.stop {
		d.stop = true
		close(d.stopCh)

		if d.conn == nil {
			return
		}

		d.conn.Close(websocket.StatusGoingAway, "")
		d.conn = nil
	}
}

func (d *Dealer) startReceiving() {
	d.recvLoopOnce.Do(func() {
		recvCtx, cancelFn := context.WithCancel(context.Background())
		go d.recvLoop(recvCtx)
		go func() {
			<-d.stopCh
			cancelFn()
		}()

		// set last pong in the future
		d.lastPong = time.Now().Add(pingInterval)
		go d.pingTicker(recvCtx)
	})
}

func (d *Dealer) pingTicker(_ctx context.Context) {
	ticker := time.NewTicker(pingInterval)

loop:
	for {
		select {
		case <-d.stopCh:
			break loop
		case <-ticker.C:
			d.lastPongLock.Lock()
			timePassed := time.Since(d.lastPong)
			d.lastPongLock.Unlock()
			if timePassed > pingInterval+timeout {
				d.log.Errorf("did not receive last pong from dealer, %.0fs passed, so close connection", timePassed.Seconds())

				// closing the connection should make the read on the "recvLoop" fail,
				// continue hoping for a new connection
				d.conn.Close(websocket.StatusServiceRestart, "")
				continue loop
			}

			ctx, cancel := context.WithTimeout(_ctx, timeout)
			err := d.conn.Write(ctx, websocket.MessageText, []byte("{\"type\":\"ping\"}"))
			cancel()
			if err != nil {
				if d.isStoped() {
					// break early without logging if we should stop
					break loop
				}

				d.log.WithError(err).Warnf("failed sending dealer ping")

				// closing the connection should make the read on the "recvLoop" fail,
				// continue hoping for a new connection
				d.conn.Close(websocket.StatusServiceRestart, "")
			} else {
				d.log.Debug("sent dealer ping")
			}
		}
	}

	ticker.Stop()
}

func (d *Dealer) isStoped() bool {
	d.lock.RLock()
	defer d.lock.RUnlock()

	return d.stop
}

func (d *Dealer) recvLoop(_ctx context.Context) {
	d.log.Debug("[ruslan] dealer recv loop begin")

	for !d.isStoped() {
		d.log.Debug("[ruslan] dealer recv message loop begin")

		for !d.isStoped() {
			// no need to hold the connMu since reconnection happens in this routine
			msgType, messageBytes, err := d.conn.Read(_ctx)
			if err != nil {
				d.log.WithError(err).Errorf("failed receiving dealer message")
				break
			} else if msgType != websocket.MessageText {
				d.log.WithError(err).Warnf("unsupported message type: %v, len: %d", msgType, len(messageBytes))
				continue
			}

			var message RawMessage
			if err := json.Unmarshal(messageBytes, &message); err != nil {
				d.log.WithError(err).Error("failed unmarshalling dealer message")
				break
			}

			d.log.Debugf("[ruslan] dealer recv message loop hadling message: %s begin", message.Type)

			switch message.Type {
			case "message":
				d.handleMessage(&message)
			case "request":
				d.handleRequest(&message)
			case "pong":
				d.lastPongLock.Lock()
				d.lastPong = time.Now()
				d.lastPongLock.Unlock()
				d.log.Debug("received dealer pong")
			default:
				d.log.Warnf("unknown dealer message type: %s", message.Type)
			}

			d.log.Debugf("[ruslan] dealer recv message loop hadling message: %s end", message.Type)
		}

		d.log.Debug("[ruslan] dealer recv message loop end")

		// if we shouldn't stop, try to reconnect
		if !d.isStoped() {
			err := backoff.Retry(func() error {
				return d.connect(_ctx)
			}, backoff.WithContext(backoff.NewExponentialBackOff(), _ctx))

			if _ctx.Err() != nil {
				break
		} else {

			if err != nil {
				d.log.WithError(err).Errorf("failed reconnecting dealer, bye bye")
				log.Exit(1)
			}
	}

			d.lastPongLock.Lock()
			d.lastPong = time.Now()
			d.lastPongLock.Unlock()

			// reconnection was successful, do not close receivers
			d.log.Debugf("re-established dealer connection")
		}
	}

	d.log.Debug("[ruslan] dealer recv loop end")
}

func (d *Dealer) sendReply(key string, success bool) error {
	reply := Reply{Type: "reply", Key: key}
	reply.Payload.Success = success

	replyBytes, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("failed marshalling reply: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	err = d.conn.Write(ctx, websocket.MessageText, replyBytes)
	cancel()
	if err != nil {
		return fmt.Errorf("failed sending dealer reply: %w", err)
	}

	return nil
}
