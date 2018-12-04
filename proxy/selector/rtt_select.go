package selector

import (
	"github.com/sipt/shuttle"
	"github.com/sipt/shuttle/log"
	"github.com/sipt/shuttle/proxy"
	"io"
	"sync/atomic"
	"time"
)

const (
	timerDulation = 10 * time.Minute
)

func init() {
	proxy.RegisterSelector("rtt", func(group *proxy.ServerGroup) (proxy.ISelector, error) {
		s := &rttSelector{
			group:    group,
			timer:    time.NewTimer(timerDulation),
			selected: group.Servers[0].(proxy.IServer),
			cancel:   make(chan bool, 1),
		}
		go func() {
			for {
				select {
				case <-s.timer.C:
				case <-s.cancel:
					return
				}
				s.autoTest()
				s.timer.Reset(timerDulation)
			}
		}()
		go s.Refresh()
		return s, nil
	})
}

type rttSelector struct {
	group    *proxy.ServerGroup
	selected proxy.IServer
	status   uint32
	timer    *time.Timer
	cancel   chan bool
}

func (r *rttSelector) Get() (*proxy.Server, error) {
	return r.selected.GetServer()
}
func (r *rttSelector) Select(name string) error {
	return nil
}
func (r *rttSelector) Refresh() error {
	r.autoTest()
	return nil
}
func (r *rttSelector) Reset(group *proxy.ServerGroup) error {
	r.group = group
	r.selected = r.group.Servers[0].(proxy.IServer)
	go r.autoTest()
	return nil
}
func (r *rttSelector) Destroy() {
	r.cancel <- true
}
func (r *rttSelector) autoTest() {
	if r.status == 0 {
		if ok := atomic.CompareAndSwapUint32(&r.status, 0, 1); !ok {
			return
		}
	}
	r.timer.Stop()
	log.Logger.Debug("[RTT-Selector] start testing ...")
	var is proxy.IServer
	var s *proxy.Server
	var err error
	c := make(chan *proxy.Server, 1)
	for _, v := range r.group.Servers {
		is = v.(proxy.IServer)
		s, err = is.GetServer()
		if err != nil {
			continue
		}
		go urlTest(s, c)
	}
	s = <-c
	log.Logger.Infof("[RTT-Select] rtt select server: [%s]", s.Name)
	r.selected = s
	r.timer.Reset(timerDulation)
	atomic.CompareAndSwapUint32(&r.status, 1, 0)
}

func urlTest(s *proxy.Server, c chan *proxy.Server) {
	var closer func()
	conn, err := s.Conn(shuttle.NewHttpRequest("tcp", "www.gstatic.com", "", "80",
		"HTTP", "", 0, nil))
	if err != nil {
		log.Logger.Debugf("[RTT-Select] [%s]  url test result: <failed> %v", s.Name, err)
		return
	}
	defer conn.Close()
	start := time.Now()
	_, err = conn.Write([]byte("GET /generate_204 HTTP/1.1\r\nHost: www.gstatic.com\r\n\r\n"))
	if err != nil {
		log.Logger.Debugf("[RTT-Select] [%s]  url test result: <failed> %v", s.Name, err)
		return
	}
	buf := make([]byte, 128)
	_, err = conn.Read(buf)
	if err != nil {
		if err != io.EOF {
			log.Logger.Debugf("[RTT-Select] [%s]  url test result: <failed> %v", s.Name, err)
		}
		return
	}
	if err == nil && string(buf[9:12]) == "204" {
		s.Rtt = time.Now().Sub(start)
		select {
		case c <- s:
		default:
		}
	} else {
		s.Rtt = 0
	}
	if err != nil {
		log.Logger.Debugf("[RTT-Select] [%s]  url test result: <failed> %v", s.Name, err)
	} else {
		log.Logger.Debugf("[RTT-Select] [%s]  RTT:[%dms]", s.Name, s.Rtt.Nanoseconds()/1000000)
	}
	if closer != nil {
		closer()
	}
}
func (r *rttSelector) Current() proxy.IServer {
	return r.selected
}