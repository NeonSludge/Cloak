package server

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/cbeuw/Cloak/internal/ecdh"
	"github.com/cbeuw/Cloak/internal/util"
	"net"
	"net/http"
)

type WebSocket struct{}

func (WebSocket) String() string                                    { return "WebSocket" }
func (WebSocket) HasRecordLayer() bool                              { return false }
func (WebSocket) UnitReadFunc() func(net.Conn, []byte) (int, error) { return util.ReadWebSocket }

func (WebSocket) handshake(reqPacket []byte, privateKey crypto.PrivateKey, originalConn net.Conn) (ai authenticationInfo, finisher func([]byte) (net.Conn, error), err error) {
	var req *http.Request
	req, err = http.ReadRequest(bufio.NewReader(bytes.NewBuffer(reqPacket)))
	if err != nil {
		err = fmt.Errorf("failed to parse first HTTP GET: %v", err)
		return
	}
	var hiddenData []byte
	hiddenData, err = base64.StdEncoding.DecodeString(req.Header.Get("hidden"))

	ai, err = unmarshalHidden(hiddenData, privateKey)
	if err != nil {
		err = fmt.Errorf("failed to unmarshal hidden data from WS into authenticationInfo: %v", err)
		return
	}

	finisher = func(sessionKey []byte) (preparedConn net.Conn, err error) {
		handler := newWsHandshakeHandler()

		// For an explanation of the following 3 lines, see the comments in websocketAux.go
		http.Serve(newWsAcceptor(originalConn, reqPacket), handler)

		<-handler.finished
		preparedConn = handler.conn
		nonce := make([]byte, 12)
		rand.Read(nonce)

		// reply: [12 bytes nonce][32 bytes encrypted session key][16 bytes authentication tag]
		encryptedKey, err := util.AESGCMEncrypt(nonce, ai.sharedSecret, sessionKey) // 32 + 16 = 48 bytes
		if err != nil {
			err = fmt.Errorf("failed to encrypt reply: %v", err)
			return
		}
		reply := append(nonce, encryptedKey...)
		_, err = preparedConn.Write(reply)
		if err != nil {
			err = fmt.Errorf("failed to write reply: %v", err)
			go preparedConn.Close()
			return
		}
		return
	}

	return
}

var ErrBadGET = errors.New("non (or malformed) HTTP GET")

func unmarshalHidden(hidden []byte, staticPv crypto.PrivateKey) (ai authenticationInfo, err error) {
	if len(hidden) < 96 {
		err = ErrBadGET
		return
	}
	ephPub, ok := ecdh.Unmarshal(hidden[0:32])
	if !ok {
		err = ErrInvalidPubKey
		return
	}

	ai.nonce = hidden[:12]

	ai.sharedSecret = ecdh.GenerateSharedSecret(staticPv, ephPub)

	ai.ciphertextWithTag = hidden[32:]
	if len(ai.ciphertextWithTag) != 64 {
		err = fmt.Errorf("%v: %v", ErrCiphertextLength, len(ai.ciphertextWithTag))
		return
	}
	return
}