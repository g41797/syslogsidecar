package e2e

import (
	"time"

	"github.com/g41797/sputnik"
	"github.com/g41797/sputnik/sidecar"
)

const (
	syslogConsumerName           = "syslogconsumer"
	syslogConsumerResponsibility = "syslogconsumer"
)

func syslogConsumerDescriptor() sputnik.BlockDescriptor {
	return sputnik.BlockDescriptor{Name: syslogConsumerName, Responsibility: syslogConsumerResponsibility}
}

func init() {
	sputnik.RegisterBlockFactory(syslogConsumerName, syslogConsumerBlockFactory)
}

func syslogConsumerBlockFactory() *sputnik.Block {
	cons := new(consumer)
	if mcf == nil {
		return nil
	}

	mc := mcf()
	if mc == nil {
		return nil
	}
	cons.mc = mc

	block := sputnik.NewBlock(
		sputnik.WithInit(cons.init),
		sputnik.WithRun(cons.run),
		sputnik.WithFinish(cons.finish),
		sputnik.WithOnConnect(cons.brokerConnected),
		sputnik.WithOnDisconnect(cons.brokerDisconnected),
	)
	return block
}

type consumer struct {
	connected bool
	cfact     sputnik.ConfFactory
	mc        sidecar.MessageConsumer
	sender    sputnik.BlockCommunicator
	shared    sputnik.ServerConnection
	stop      chan struct{}
	done      chan struct{}
	conn      chan sputnik.ServerConnection
	dscn      chan struct{}
}

// Init
func (cons *consumer) init(fact sputnik.ConfFactory) error {
	cons.cfact = fact
	cons.stop = make(chan struct{}, 1)
	cons.done = make(chan struct{}, 1)
	cons.conn = make(chan sputnik.ServerConnection, 1)
	cons.dscn = make(chan struct{}, 1)

	return nil
}

// Finish:
func (cons *consumer) finish(init bool) {
	if init {
		return
	}

	close(cons.stop) // Cancel Run

	<-cons.done // Wait finish of Run
	return
}

// OnServerConnect:
func (cons *consumer) brokerConnected(srvc sputnik.ServerConnection) {
	cons.conn <- srvc
	return
}

// OnServerDisconnect:
func (cons *consumer) brokerDisconnected() {
	cons.dscn <- struct{}{}
	return
}

// Run
func (cons *consumer) run(bc sputnik.BlockCommunicator) {

	cons.sender, _ = bc.Communicator(syslogClientResponsibility)

	defer close(cons.done)
	defer cons.disconnect()

waitBroker:
	for {
		select {
		case <-cons.stop:
			return
		case cons.shared = <-cons.conn:
			break waitBroker
		}
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

waitConsumer:
	for {
		select {
		case <-cons.stop:
			return
		case <-cons.dscn:
			return
		case <-ticker.C:
			if cons.mc.Connect(cons.cfact, cons.shared) == nil {
				cons.mc.Consume(cons.sender)
				break waitConsumer
			}
		}
	}

	cons.runLoop()

	return
}

func (cons *consumer) runLoop() {
loop:
	for {
		select {
		case <-cons.stop:
			break loop
		case <-cons.dscn:
			break loop
		}
	}
	return
}
func (cons *consumer) disconnect() {
	if cons == nil {
		return
	}
	if cons.mc == nil {
		return
	}
	cons.mc.Disconnect()
}

func RegisterMessageConsumerFactory(fact func() sidecar.MessageConsumer) {
	mcf = fact
}

var mcf func() sidecar.MessageConsumer
