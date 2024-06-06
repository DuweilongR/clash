package ntp

import (
	"context"
	"github.com/Dreamacro/clash/addons/metacubex/component/proxydialer"
	"sync"
	"time"

	D "github.com/Dreamacro/clash/component/dialer"
	"github.com/Dreamacro/clash/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/ntp"
)

var offset time.Duration
var service *Service

type Service struct {
	server         M.Socksaddr
	dialer         proxydialer.SingDialer
	ticker         *time.Ticker
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	syncSystemTime bool
	running        bool
}

func ReCreateNTPService(server string, interval time.Duration, dialerProxy string, syncSystemTime bool) {
	if service != nil {
		service.Stop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	service = &Service{
		server:         M.ParseSocksaddr(server),
		dialer:         proxydialer.NewByNameSingDialer(dialerProxy, D.NewDialer()),
		ticker:         time.NewTicker(interval * time.Minute),
		ctx:            ctx,
		cancel:         cancel,
		syncSystemTime: syncSystemTime,
	}
	service.Start()
}

func (srv *Service) Start() {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	log.Infoln("NTP service start, sync system time is %t", srv.syncSystemTime)
	err := srv.update()
	if err != nil {
		log.Errorln("Initialize NTP time failed: %s", err)
		return
	}
	service.running = true
	go srv.loopUpdate()
}

func (srv *Service) Stop() {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if service.running {
		srv.ticker.Stop()
		srv.cancel()
		service.running = false
	}
}

func (srv *Service) Running() bool {
	if srv == nil {
		return false
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.running
}

func (srv *Service) update() error {
	var response *ntp.Response
	var err error
	for i := 0; i < 3; i++ {
		if response, err = ntp.Exchange(context.Background(), srv.dialer, srv.server); err == nil {
			break
		}
		if i == 2 {
			return err
		}
	}
	offset = response.ClockOffset
	if offset > time.Duration(0) {
		log.Infoln("System clock is ahead of NTP time by %s", offset)
	} else if offset < time.Duration(0) {
		log.Infoln("System clock is behind NTP time by %s", -offset)
	}
	if srv.syncSystemTime {
		timeNow := response.Time
		syncErr := setSystemTime(timeNow)
		if syncErr == nil {
			log.Infoln("Sync system time success: %s", timeNow.Local().Format(ntp.TimeLayout))
		} else {
			log.Errorln("Write time to system: %s", syncErr)
			srv.syncSystemTime = false
		}
	}
	return nil
}

func (srv *Service) loopUpdate() {
	for {
		select {
		case <-srv.ctx.Done():
			return
		case <-srv.ticker.C:
		}
		err := srv.update()
		if err != nil {
			log.Warnln("Sync time failed: %s", err)
		}
	}
}

func Now() time.Time {
	now := time.Now()
	if service.Running() && abs(offset) > 0 {
		now = now.Add(offset)
	}
	return now
}

const (
	minDuration time.Duration = -1 << 63
	maxDuration time.Duration = 1<<63 - 1
)

// abs returns the absolute value of d.
// As a special case, math.MinInt64 is converted to math.MaxInt64.
func abs(d time.Duration) time.Duration {
	switch {
	case d >= 0:
		return d
	case d == minDuration:
		return maxDuration
	default:
		return -d
	}
}
