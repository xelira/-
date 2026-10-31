package faketls

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"sync/atomic"
	"time"
)

// Server-side Fake TLS answer modelled on Flowseal/tg-ws-proxy v1.11.3
// (#1473): mirror what the client offered (cipher suite, key share group) and
// size the single ApplicationData record like the real masking site does, so
// the flight does not look like the classic fixed-size MTProxy answer.

const (
	extSupportedVersions = 0x002b
	extKeyShare          = 0x0033

	groupX25519          = 0x001d
	groupX25519MLKEM768  = 0x11ec
	x25519KeyLen         = 32
	mlkem768ClientShare  = 1216 // client share: ML-KEM-768 encaps key + X25519
	mlkem768Ciphertext   = 1088
	appDataMin           = 64
	fallbackAppDataLow   = 64
	fallbackAppDataSpan  = 97 // 64..160 inclusive
	defaultProbeTimeout  = 5 * time.Second
	recordHeaderLen      = 5
	clientHelloFixedSkip = recordHeaderLen + 4 + 2 + 32 // record hdr + hs hdr + version + random
)

// TLS 1.3 suites in the order a typical server picks them from the client list.
var cipherPreference = [...]uint16{0x1301, 0x1303, 0x1302}

// ClientHelloInfo is what the server hello mirrors.
type ClientHelloInfo struct {
	CipherSuites map[uint16]bool
	KeyShares    map[uint16]int // named group -> key_exchange length
}

// Hello is a validated ClientHello.
type Hello struct {
	Random    []byte
	SessionID []byte
	Info      ClientHelloInfo
}

var profiledAppDataLen atomic.Int32 // 0 = not profiled

// SetProfiledAppDataLen fixes the ApplicationData record size (0 clears it).
func SetProfiledAppDataLen(n int) {
	switch {
	case n <= 0:
		profiledAppDataLen.Store(0)
	case n < appDataMin:
		profiledAppDataLen.Store(appDataMin)
	case n > MaxRecordPayload:
		profiledAppDataLen.Store(MaxRecordPayload)
	default:
		profiledAppDataLen.Store(int32(n))
	}
}

func appDataLen() int {
	if n := profiledAppDataLen.Load(); n > 0 {
		return int(n)
	}
	r, err := rand.Int(rand.Reader, big.NewInt(fallbackAppDataSpan))
	if err != nil {
		return fallbackAppDataLow
	}
	return fallbackAppDataLow + int(r.Int64())
}

// ParseClientHello extracts the session id, offered cipher suites and key_share
// groups from a full ClientHello record (5-byte header included). Malformed
// input yields ok=false and empty info, so callers fall back to plain X25519.
func ParseClientHello(record []byte) (sessionID []byte, info ClientHelloInfo, ok bool) {
	info = ClientHelloInfo{CipherSuites: map[uint16]bool{}, KeyShares: map[uint16]int{}}
	data := record
	pos := clientHelloFixedSkip
	if len(data) < pos+1 {
		return nil, info, false
	}
	sidLen := int(data[pos])
	pos++
	if pos+sidLen > len(data) {
		return nil, info, false
	}
	sessionID = append([]byte(nil), data[pos:pos+sidLen]...)
	pos += sidLen

	if pos+2 > len(data) {
		return sessionID, info, false
	}
	suitesLen := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if pos+suitesLen > len(data) {
		return sessionID, info, false
	}
	for off := pos; off+1 < pos+suitesLen; off += 2 {
		info.CipherSuites[binary.BigEndian.Uint16(data[off:])] = true
	}
	pos += suitesLen

	if pos+1 > len(data) {
		return sessionID, info, false
	}
	pos += 1 + int(data[pos]) // compression methods

	if pos+2 > len(data) {
		return sessionID, info, false
	}
	extEnd := pos + 2 + int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if extEnd > len(data) {
		extEnd = len(data)
	}
	for pos+4 <= extEnd {
		typ := binary.BigEndian.Uint16(data[pos:])
		n := int(binary.BigEndian.Uint16(data[pos+2:]))
		pos += 4
		if typ == extKeyShare && pos+2 <= extEnd {
			sharesEnd := pos + 2 + int(binary.BigEndian.Uint16(data[pos:]))
			if sharesEnd > extEnd {
				sharesEnd = extEnd
			}
			for off := pos + 2; off+4 <= sharesEnd; {
				group := binary.BigEndian.Uint16(data[off:])
				klen := int(binary.BigEndian.Uint16(data[off+2:]))
				off += 4 + klen
				if off <= sharesEnd {
					if _, seen := info.KeyShares[group]; !seen {
						info.KeyShares[group] = klen
					}
				}
			}
		}
		pos += n
	}
	return sessionID, info, true
}

func selectCipherSuite(info ClientHelloInfo) uint16 {
	for _, s := range cipherPreference {
		if info.CipherSuites[s] {
			return s
		}
	}
	return cipherPreference[0]
}

func x25519Public() []byte {
	if k, err := ecdh.X25519().GenerateKey(rand.Reader); err == nil {
		return k.PublicKey().Bytes()
	}
	b := make([]byte, x25519KeyLen)
	_, _ = rand.Read(b)
	b[len(b)-1] &= 0x7f
	return b
}

func serverKeyShare(info ClientHelloInfo) (group uint16, key []byte) {
	// Mirror the hybrid post-quantum group when the client offers it with the
	// expected size; the client never derives keys from this fake answer.
	if info.KeyShares[groupX25519MLKEM768] == mlkem768ClientShare {
		ct := make([]byte, mlkem768Ciphertext)
		_, _ = rand.Read(ct)
		return groupX25519MLKEM768, append(ct, x25519Public()...)
	}
	return groupX25519, x25519Public()
}

// BuildServerHello builds ServerHello + ChangeCipherSpec + one ApplicationData
// record and signs it: server_random = HMAC(secret, clientRandom || flight with
// zeroed random), which Telegram clients verify over all three records.
func BuildServerHello(secret []byte, h *Hello) []byte {
	group, key := serverKeyShare(h.Info)

	var ext []byte
	ext = appendU16(ext, extSupportedVersions)
	ext = appendU16(ext, 2)
	ext = appendU16(ext, 0x0304)
	ext = appendU16(ext, extKeyShare)
	ext = appendU16(ext, uint16(4+len(key)))
	ext = appendU16(ext, group)
	ext = appendU16(ext, uint16(len(key)))
	ext = append(ext, key...)

	var body []byte
	body = append(body, version12[:]...)
	body = append(body, make([]byte, 32)...) // random, filled by the signature
	body = append(body, byte(len(h.SessionID)))
	body = append(body, h.SessionID...)
	body = appendU16(body, selectCipherSuite(h.Info))
	body = append(body, 0x00) // compression: null
	body = appendU16(body, uint16(len(ext)))
	body = append(body, ext...)

	var hs []byte
	hs = append(hs, 0x02, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	hs = append(hs, body...)

	var out []byte
	out = append(out, RecordHandshake, version12[0], version12[1])
	out = appendU16(out, uint16(len(hs)))
	out = append(out, hs...)
	out = append(out, RecordChangeCipherSpec, version12[0], version12[1], 0x00, 0x01, 0x01)

	n := appDataLen()
	app := make([]byte, n)
	_, _ = rand.Read(app)
	out = append(out, RecordApplicationData, version12[0], version12[1])
	out = appendU16(out, uint16(n))
	out = append(out, app...)

	mac := hmac.New(sha256.New, secret)
	mac.Write(h.Random)
	mac.Write(out)
	copy(out[digestPos:digestPos+digestLen], mac.Sum(nil))
	return out
}

// SendServerHello writes BuildServerHello's flight to w.
func SendServerHello(w io.Writer, secret []byte, h *Hello) error {
	_, err := w.Write(BuildServerHello(secret, h))
	return err
}

// FirstAppDataRecordLen returns the length of the first ApplicationData record
// in a server's first flight. ok=false means more data is needed; a zero length
// with ok=true means the flight cannot contain one (alert, TLS 1.2, garbage).
func FirstAppDataRecordLen(flight []byte) (n int, ok bool) {
	pos := 0
	for pos+recordHeaderLen <= len(flight) {
		typ := flight[pos]
		recLen := int(binary.BigEndian.Uint16(flight[pos+3:]))
		if typ == RecordApplicationData {
			return recLen, true
		}
		if typ != RecordHandshake && typ != RecordChangeCipherSpec {
			return 0, true
		}
		pos += recordHeaderLen + recLen
	}
	return 0, false
}

// clientHelloFor returns the ClientHello bytes a TLS 1.3 client would send to
// domain, captured without ever completing the handshake.
func clientHelloFor(domain string) ([]byte, error) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := tls.Client(a, &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h2", "http/1.1"},
	})
	go func() { _ = c.Handshake() }() // blocks after writing the ClientHello
	hdr := make([]byte, recordHeaderLen)
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(b, hdr); err != nil {
		return nil, err
	}
	rest := make([]byte, binary.BigEndian.Uint16(hdr[3:]))
	if _, err := io.ReadFull(b, rest); err != nil {
		return nil, err
	}
	return append(hdr, rest...), nil
}

// ProbeAppDataLen measures the first encrypted record the masking site sends
// after its ServerHello. Only the raw first flight is read.
func ProbeAppDataLen(ctx context.Context, domain, port string, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	hello, err := clientHelloFor(domain)
	if err != nil {
		return 0, err
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(domain, port))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(hello); err != nil {
		return 0, err
	}
	var flight []byte
	buf := make([]byte, 16384)
	for {
		n, err := conn.Read(buf)
		flight = append(flight, buf[:n]...)
		if l, ok := FirstAppDataRecordLen(flight); ok {
			if l == 0 {
				return 0, io.ErrUnexpectedEOF
			}
			return l, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
