package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/cbeuw/Cloak/internal/util"
	"time"

	log "github.com/sirupsen/logrus"
)

type ClientInfo struct {
	UID              []byte
	SessionId        uint32
	ProxyMethod      string
	EncryptionMethod byte
	Unordered        bool
	Transport        Transport
}

type authFragments struct {
	sharedSecret      [32]byte
	randPubKey        [32]byte
	ciphertextWithTag [64]byte
}

const (
	UNORDERED_FLAG = 0x01 // 0000 0001
)

var ErrTimestampOutOfWindow = errors.New("timestamp is outside of the accepting window")
var ErrUnreconisedProtocol = errors.New("unreconised protocol")

// decryptClientInfo checks if a the authFragments are valid. It doesn't check if the UID is authorised
func decryptClientInfo(fragments authFragments, now func() time.Time) (info ClientInfo, err error) {
	var plaintext []byte
	plaintext, err = util.AESGCMDecrypt(fragments.randPubKey[0:12], fragments.sharedSecret[:], fragments.ciphertextWithTag[:])
	if err != nil {
		return
	}

	info = ClientInfo{
		UID:              plaintext[0:16],
		SessionId:        0,
		ProxyMethod:      string(bytes.Trim(plaintext[16:28], "\x00")),
		EncryptionMethod: plaintext[28],
		Unordered:        plaintext[41]&UNORDERED_FLAG != 0,
	}

	timestamp := int64(binary.BigEndian.Uint64(plaintext[29:37]))
	clientTime := time.Unix(timestamp, 0)
	serverTime := now()
	if !(clientTime.After(serverTime.Truncate(TIMESTAMP_TOLERANCE)) && clientTime.Before(serverTime.Add(TIMESTAMP_TOLERANCE))) {
		err = fmt.Errorf("%v: received timestamp %v", ErrTimestampOutOfWindow, timestamp)
		return
	}
	info.SessionId = binary.BigEndian.Uint32(plaintext[37:41])
	return
}

var ErrReplay = errors.New("duplicate random")
var ErrBadProxyMethod = errors.New("invalid proxy method")

// AuthFirstPacket checks if the first packet of data is ClientHello or HTTP GET, and checks if it was from a Cloak client
// if it is from a Cloak client, it returns the ClientInfo with the decrypted fields. It doesn't check if the user
// is authorised. It also returns a finisher callback function to be called when the caller wishes to proceed with
// the handshake
func AuthFirstPacket(firstPacket []byte, sta *State) (info ClientInfo, finisher Responder, err error) {
	var transport Transport
	switch firstPacket[0] {
	case 0x47:
		transport = &WebSocket{}
	case 0x16:
		transport = &TLS{}
	default:
		err = ErrUnreconisedProtocol
		return
	}

	fragments, finisher, err := transport.processFirstPacket(firstPacket, sta.staticPv)
	if err != nil {
		return
	}

	if sta.registerRandom(fragments.randPubKey) {
		err = ErrReplay
		return
	}

	info, err = decryptClientInfo(fragments, sta.Now)
	if err != nil {
		log.Debug(err)
		err = fmt.Errorf("transport %v in correct format but not Cloak: %v", transport, err)
		return
	}
	if _, ok := sta.ProxyBook[info.ProxyMethod]; !ok {
		err = ErrBadProxyMethod
		return
	}
	info.Transport = transport
	return
}
