package publishbot

import (
	"bufio"
	"net"
	"sync"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name: "publishbot",
	Help: `Listens on a TCP port for lines of the form "password:#channel:text".

	The received text is forwarded to all configured plugin targets that contain
	the provided "password:#channel" in the "accept" list in their configuration.

	See https://launchpad.net/publish-bot for the original implementation.
	`,
	Start: start,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type pbotPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	accept   map[string][]*mup.PluginTarget
	listener net.Listener
	config   struct {
		Addr string
	}
}

const defaultAddr = ":10234"

func start(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &pbotPlugin{
		plugger: plugger,
		accept:  make(map[string][]*mup.PluginTarget),
	}
	p.plugger.Config(&p.config)
	if p.config.Addr == "" {
		p.config.Addr = defaultAddr
	}
	targets := plugger.Targets()
	var config struct {
		Accept []string
	}
	for i := range targets {
		t := &targets[i]
		t.Config(&config)
		for _, prefix := range config.Accept {
			p.accept[prefix] = append(p.accept[prefix], t)
		}
	}
	p.tomb.Go(p.loop)
	return p, nil
}

func (p *pbotPlugin) Stop() error {
	p.tomb.Kill(nil)
	p.mu.Lock()
	if p.listener != nil {
		p.listener.Close()
	}
	p.mu.Unlock()
	p.plugger.Logf("Waiting.")
	return p.tomb.Wait()
}

func (p *pbotPlugin) loop() error {
	first := true
	for p.tomb.Alive() {
		l, err := net.Listen("tcp", p.config.Addr)
		if err != nil {
			if first {
				first = false
				p.plugger.Logf("Cannot listen on %s (%v). Will keep retrying.", p.config.Addr, err)
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		p.plugger.Logf("Listening on %s.", p.config.Addr)

		p.mu.Lock()
		p.listener = l
		p.mu.Unlock()

		for p.tomb.Alive() {
			conn, err := l.Accept()
			if err != nil {
				if p.tomb.Alive() {
					p.plugger.Logf("Failed to accept a connection: %v", err)
				}
				break
			}

			p.tomb.Go(func() error {
				p.handle(conn)
				return nil
			})
		}

		l.Close()
	}
	return nil
}

func (p *pbotPlugin) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		j, n := 0, 0
		for i, c := range line {
			if c != ':' {
				continue
			}
			n++
			if n == 2 {
				j = i
				break
			}
		}
		if n != 2 {
			continue
		}
		text := line[j+1:]
		for _, target := range p.accept[line[:j]] {
			if !target.CanSend() {
				continue
			}
			p.plugger.Logf("Forwarding message to %s: %s", target, text)
			err := p.plugger.Sendf(target, "%s", text)
			if err != nil {
				p.plugger.Logf("Cannot forward received message: %v", err)
			}
		}
	}
	if scanner.Err() != nil {
		p.plugger.Debugf("Line scanner stopped with an error: %v", scanner.Err())
	}
}
