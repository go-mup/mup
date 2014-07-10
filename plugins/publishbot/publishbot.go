package publishbot

import (
	"bufio"
	"net"
	"sync"
	"time"

	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/tomb.v2"
)

func init() {
	mup.RegisterPlugin("publishbot", startPlugin)
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

func startPlugin(plugger *mup.Plugger) mup.Plugin {
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
	return p
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

func (p *pbotPlugin) Handle(msg *mup.Message) error {
	return nil
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
			if target.Target == "" {
				continue
			}
			p.plugger.Logf("Forwarding message to %s's %s: %s", target.Account, target.Target, text)
			err := p.plugger.Send(&mup.Message{
				Account: target.Account,
				Target:  target.Target,
				Text:    text,
			})
			if err != nil {
				p.plugger.Logf("Cannot forward received message: %v", err)
			}
		}
	}
	if scanner.Err() != nil {
		p.plugger.Debugf("Line scanner stopped with an error: %v", scanner.Err())
	}
}
