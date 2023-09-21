package syslogsidecar

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/g41797/sputnik"
	"gopkg.in/mcuadros/go-syslog.v2"
	"gopkg.in/mcuadros/go-syslog.v2/format"
)

type SyslogConfiguration struct {
	// The Syslog Severity level ranges between 0 to 7.
	// Each number points to the relevance of the action reported.
	// From a debugging message (7) to a completely unusable system (0):
	//
	//	0		Emergency: system is unusable
	//	1		Alert: action must be taken immediately
	//	2		Critical: critical conditions
	//	3		Error: error conditions
	//	4		Warning: warning conditions
	//	5		Notice: normal but significant condition
	//	6		Informational: informational messages
	//	7		Debug: debug-level messages
	//
	// Log with severity above value from configuration will be discarded
	// Examples:
	// -1 - all logs will be discarded
	// 5  - logs with severities 6(Informational) and 7(Debug) will be discarded
	// 7  - all logs will be processed
	SEVERITYLEVEL int

	// IPv4 address of TCP listener.
	// For empty string - don't use TCP
	// e.g "0.0.0.0:5141" - listen on all adapters, port 5141
	// "127.0.0.1:5141" - listen on loopback "adapter"
	ADDRTCP string

	// IPv4 address of UDP receiver.
	// For empty string - don't use UDP
	// Usually "0.0.0.0:5141" - receive from all adapters, port 5141
	// "127.0.0.1:5141" - receive from loopback "adapter"
	ADDRUDP string

	// Unix domain socket name - actually file path.
	// For empty string - don't use UDS
	// Regarding limitations see https://man7.org/linux/man-pages/man7/unix.7.html
	UDSPATH string

	// TLS section: Listening on non empty ADDRTCPTLS will start only
	// for valid tls configuration (created using last 3 parameters)
	ADDRTCPTLS       string
	CLIENT_CERT_PATH string
	CLIENT_KEY_PATH  string
	ROOT_CA_PATH     string
}

type Server struct {
	config  SyslogConfiguration
	bc      atomic.Pointer[sputnik.BlockCommunicator]
	syslogd *syslog.Server
}

func NewServer(conf SyslogConfiguration) *Server {
	srv := new(Server)
	srv.config = conf
	srv.bc = atomic.Pointer[sputnik.BlockCommunicator]{}
	return srv
}

func (s *Server) Init() error {
	s.syslogd = syslog.NewServer()
	s.syslogd.SetFormat(syslog.Automatic)
	s.syslogd.SetHandler(s)
	if len(s.config.ADDRTCP) != 0 {
		err := s.syslogd.ListenTCP(s.config.ADDRTCP)
		if err != nil {
			return err
		}
	}

	if len(s.config.ADDRUDP) != 0 {
		err := s.syslogd.ListenUDP(s.config.ADDRUDP)
		if err != nil {
			return err
		}
	}

	if len(s.config.ADDRTCPTLS) != 0 {
		t, err := PrepareTLS(s.config.CLIENT_CERT_PATH, s.config.CLIENT_KEY_PATH, s.config.ROOT_CA_PATH)

		if err != nil {
			return err
		}

		if t != nil {
			err = s.syslogd.ListenTCPTLS(s.config.ADDRUDP, t)
			if err != nil {
				return err
			}
		}
	}

	if len(s.config.UDSPATH) != 0 {
		err := s.syslogd.ListenUnixgram(s.config.UDSPATH)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) Start() error {
	return s.syslogd.Boot()
}

func (s *Server) Finish() error {
	err := s.syslogd.Kill()
	return err
}

func (s *Server) SetupHandling(bc sputnik.BlockCommunicator) {
	s.bc.Store(&bc)
}

func (s *Server) Handle(logParts format.LogParts, msgLen int64, err error) {
	if s.bc.Load() == nil {
		return
	}

	if err != nil {
		return
	}

	if !s.forHandle(logParts) {
		return
	}

	msg := toMsg(logParts, msgLen)

	(*s.bc.Load()).Send(msg)
}

func (s *Server) forHandle(logParts format.LogParts) bool {
	if s.config.SEVERITYLEVEL == -1 {
		return false
	}

	if logParts == nil {
		return false
	}

	if len(logParts) == 0 {
		return false
	}

	severity, exists := logParts[SeverityKey]

	if !exists {
		return true
	}

	sevvalue, _ := severity.(int)

	return sevvalue <= s.config.SEVERITYLEVEL
}

func toMsg(logParts format.LogParts, msgLen int64) sputnik.Msg {
	if logParts == nil {
		return nil
	}

	if len(logParts) == 0 {
		return nil
	}

	_, exists := logParts[RFC5424OnlyKey]

	if exists {
		return toRFC5424(logParts)
	} else {
		return toRFC3164(logParts)
	}
}

// Convert syslog RFC5424 values to strings
func toRFC5424(logParts format.LogParts) sputnik.Msg {
	msg := make(sputnik.Msg)
	msg[RFCFormatKey] = RFC5424

	props := RFC5424Props()

	for k, v := range logParts {
		msg[k] = toString(v, props[k])
	}

	return msg
}

// Convert syslog RFC3164 values to strings
func toRFC3164(logParts format.LogParts) sputnik.Msg {
	msg := make(sputnik.Msg)
	msg[RFCFormatKey] = RFC3164

	props := RFC3164Props()

	for k, v := range logParts {
		msg[k] = toString(v, props[k])
	}

	return msg
}

func toString(val any, typ string) string {
	result := ""

	if val == nil {
		return result
	}

	switch typ {
	case "string":
		result, _ = val.(string)
		return result
	case "int":
		intval, _ := val.(int)
		result = strconv.Itoa(intval)
		return result
	case "time.Time":
		tval, _ := val.(time.Time)
		result = tval.UTC().String()
		return result
	}

	return result
}

// RFC3164 parameters with type
func RFC3164Props() map[string]string {
	return map[string]string{
		"priority":  "int",
		"facility":  "int",
		SeverityKey: "int",
		"timestamp": "time.Time",
		"hostname":  "string",
		"tag":       "string",
		"content":   "string",
	}
}

// RFC5424 parameters with type
func RFC5424Props() map[string]string {
	return map[string]string{
		"priority":     "int",
		"facility":     "int",
		SeverityKey:    "int",
		"timestamp":    "time.Time",
		"hostname":     "string",
		"version":      "int",
		"app_name":     "string",
		"proc_id":      "string",
		"msg_id":       "string",
		RFC5424OnlyKey: "string",
		"message":      "string",
	}
}

const (
	RFC5424OnlyKey = "structured_data"
	RFCFormatKey   = "rfc"
	RFC3164        = "RFC3164"
	RFC5424        = "RFC5424"
	SeverityKey    = "severity"
)

func PrepareTLS(CLIENT_CERT_PATH, CLIENT_KEY_PATH, ROOT_CA_PATH string) (*tls.Config, error) {

	if CLIENT_CERT_PATH == "" || CLIENT_KEY_PATH != "" || ROOT_CA_PATH != "" {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(CLIENT_CERT_PATH, CLIENT_KEY_PATH)
	if err != nil {
		return nil, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	TLSConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	TLSConfig.Certificates = []tls.Certificate{cert}
	certs := x509.NewCertPool()

	pemData, err := os.ReadFile(ROOT_CA_PATH)
	if err != nil {
		return nil, err
	}
	certs.AppendCertsFromPEM(pemData)
	TLSConfig.RootCAs = certs

	return TLSConfig, nil
}