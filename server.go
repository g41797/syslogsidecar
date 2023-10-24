package syslogsidecar

import (
	"sync/atomic"

	"github.com/g41797/go-syslog"
	"github.com/g41797/go-syslog/format"
	"github.com/g41797/kissngoqueue"
	"github.com/g41797/sputnik"
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

type server struct {
	config  SyslogConfiguration
	bc      atomic.Pointer[sputnik.BlockCommunicator]
	syslogd *syslog.Server
	q       *kissngoqueue.Queue[format.LogParts]
}

func newServer(conf SyslogConfiguration) *server {
	srv := new(server)
	srv.config = conf
	srv.bc = atomic.Pointer[sputnik.BlockCommunicator]{}
	srv.q = kissngoqueue.NewQueue[format.LogParts]()
	return srv
}

func (s *server) Init() error {
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
		t, err := prepareTLS(s.config.CLIENT_CERT_PATH, s.config.CLIENT_KEY_PATH, s.config.ROOT_CA_PATH)

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

	go s.processLogParts()

	return nil
}

func (s *server) Start() error {
	return s.syslogd.Boot()
}

func (s *server) Finish() error {
	s.q.Cancel()
	return s.syslogd.Kill()
}

func (s *server) SetupHandling(bc sputnik.BlockCommunicator) {
	s.bc.Store(&bc)
}

func (s *server) Handle(logParts format.LogParts, msgLen int64, err error) {
	if s.bc.Load() == nil {
		return
	}

	if (err == nil) && (!s.forHandle(logParts)) {
		return
	}

	s.q.PutMT(logParts)
}

func (s *server) startLogPartsProcessor() {

}

func (s *server) processLogParts() {
	for {
		lp, ok := s.q.Get()
		if !ok {
			break
		}
		(*s.bc.Load()).Send(toMsg(lp))
	}
	return
}

func (s *server) forHandle(logParts format.LogParts) bool {
	if s.config.SEVERITYLEVEL == -1 {
		return false
	}

	if logParts == nil {
		return false
	}

	if len(logParts) == 0 {
		return false
	}

	severity, exists := logParts[severityKey]

	if !exists {
		return true
	}

	sevvalue, _ := severity.(int)

	return sevvalue <= s.config.SEVERITYLEVEL
}
