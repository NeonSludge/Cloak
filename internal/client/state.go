package client

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"strings"
	"time"

	"github.com/cbeuw/Cloak/internal/ecdh"
	mux "github.com/cbeuw/Cloak/internal/multiplex"
)

// rawConfig represents the fields in the config json file
// nullable means if it's empty, a default value will be chosen in SplitConfigs
// jsonOptional means if the json's empty, its value will be set from environment variables or commandline args
// but it mustn't be empty when SplitConfigs is called
type rawConfig struct {
	ServerName       string
	ProxyMethod      string
	EncryptionMethod string
	UID              []byte
	PublicKey        []byte
	NumConn          int
	LocalHost        string // jsonOptional
	LocalPort        string // jsonOptional
	RemoteHost       string // jsonOptional
	RemotePort       string // jsonOptional

	// defaults set in SplitConfigs
	UDP           bool   // nullable
	BrowserSig    string // nullable
	Transport     string // nullable
	StreamTimeout int    // nullable
	KeepAlive     int    // nullable
}

type localConnConfig struct {
	LocalAddr string
	Timeout   time.Duration
}

// semi-colon separated value. This is for Android plugin options
func ssvToJson(ssv string) (ret []byte) {
	elem := func(val string, lst []string) bool {
		for _, v := range lst {
			if val == v {
				return true
			}
		}
		return false
	}
	unescape := func(s string) string {
		r := strings.Replace(s, `\\`, `\`, -1)
		r = strings.Replace(r, `\=`, `=`, -1)
		r = strings.Replace(r, `\;`, `;`, -1)
		return r
	}
	unquoted := []string{"NumConn", "StreamTimeout", "KeepAlive", "UDP"}
	lines := strings.Split(unescape(ssv), ";")
	ret = []byte("{")
	for _, ln := range lines {
		if ln == "" {
			break
		}
		sp := strings.SplitN(ln, "=", 2)
		key := sp[0]
		value := sp[1]
		// JSON doesn't like quotation marks around int and bool
		// This is extremely ugly but it's still better than writing a tokeniser
		if elem(key, unquoted) {
			ret = append(ret, []byte(`"`+key+`":`+value+`,`)...)
		} else {
			ret = append(ret, []byte(`"`+key+`":"`+value+`",`)...)
		}
	}
	ret = ret[:len(ret)-1] // remove the last comma
	ret = append(ret, '}')
	return ret
}

func ParseConfig(conf string) (raw *rawConfig, err error) {
	var content []byte
	// Checking if it's a path to json or a ssv string
	if strings.Contains(conf, ";") && strings.Contains(conf, "=") {
		content = ssvToJson(conf)
	} else {
		content, err = ioutil.ReadFile(conf)
		if err != nil {
			return
		}
	}

	raw = new(rawConfig)
	err = json.Unmarshal(content, &raw)
	if err != nil {
		return
	}
	return
}

func (raw *rawConfig) SplitConfigs() (local *localConnConfig, remote *remoteConnConfig, auth *authInfo, err error) {
	nullErr := func(field string) (local *localConnConfig, remote *remoteConnConfig, auth *authInfo, err error) {
		err = fmt.Errorf("%v cannot be empty", field)
		return
	}

	auth = new(authInfo)
	auth.UID = raw.UID
	auth.Unordered = raw.UDP
	if raw.ServerName == "" {
		return nullErr("ServerName")
	}
	auth.MockDomain = raw.ServerName
	if raw.ProxyMethod == "" {
		return nullErr("ServerName")
	}
	auth.ProxyMethod = raw.ProxyMethod
	if len(raw.UID) == 0 {
		return nullErr("UID")
	}

	// static public key
	if len(raw.PublicKey) == 0 {
		return nullErr("PublicKey")
	}
	pub, ok := ecdh.Unmarshal(raw.PublicKey)
	if !ok {
		err = fmt.Errorf("failed to unmarshal Public key")
		return
	}
	auth.ServerPubKey = pub

	// Encryption method
	switch strings.ToLower(raw.EncryptionMethod) {
	case "plain":
		auth.EncryptionMethod = mux.E_METHOD_PLAIN
	case "aes-gcm":
		auth.EncryptionMethod = mux.E_METHOD_AES_GCM
	case "chacha20-poly1305":
		auth.EncryptionMethod = mux.E_METHOD_CHACHA20_POLY1305
	default:
		err = fmt.Errorf("unknown encryption method %v", raw.EncryptionMethod)
		return
	}

	remote = new(remoteConnConfig)
	if raw.RemoteHost == "" {
		return nullErr("RemoteHost")
	}
	if raw.RemotePort == "" {
		return nullErr("RemotePort")
	}
	remote.RemoteAddr = net.JoinHostPort(raw.RemoteHost, raw.RemotePort)
	if raw.NumConn == 0 {
		return nullErr("NumConn")
	}
	remote.NumConn = raw.NumConn

	// Transport and (if TLS mode), browser
	switch strings.ToLower(raw.Transport) {
	case "cdn":
		remote.Transport = WSOverTLS{remote.RemoteAddr}
	case "direct":
		fallthrough
	default:
		var browser browser
		switch strings.ToLower(raw.BrowserSig) {
		case "firefox":
			browser = &Firefox{}
		case "chrome":
			fallthrough
		default:
			browser = &Chrome{}
		}
		remote.Transport = DirectTLS{browser}
	}

	// KeepAlive
	if raw.KeepAlive <= 0 {
		remote.KeepAlive = -1
	} else {
		remote.KeepAlive = remote.KeepAlive * time.Second
	}

	local = new(localConnConfig)

	if raw.LocalHost == "" {
		return nullErr("LocalHost")
	}
	if raw.LocalPort == "" {
		return nullErr("LocalPort")
	}
	local.LocalAddr = net.JoinHostPort(raw.LocalHost, raw.LocalPort)
	// stream no write timeout
	if raw.StreamTimeout == 0 {
		local.Timeout = 300 * time.Second
	} else {
		local.Timeout = time.Duration(raw.StreamTimeout) * time.Second
	}

	return
}
