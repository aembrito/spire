package svid

import (
	"crypto/ecdsa"
	"crypto/x509"
	"net/url"
	"sync"
	"time"

	"github.com/andres-erbsen/clock"
	"github.com/imkira/go-observer"
	"github.com/sirupsen/logrus"
	"github.com/spiffe/spire/pkg/agent/catalog"
	"github.com/spiffe/spire/pkg/agent/client"
	"github.com/spiffe/spire/pkg/agent/common/backoff"
	"github.com/spiffe/spire/pkg/agent/manager/cache"
	"github.com/spiffe/spire/pkg/common/telemetry"
)

const DefaultRotatorInterval = 5 * time.Second

type RotatorConfig struct {
	Catalog     catalog.Catalog
	Log         logrus.FieldLogger
	Metrics     telemetry.Metrics
	TrustDomain url.URL
	ServerAddr  string
	// Initial SVID and key
	SVID    []*x509.Certificate
	SVIDKey *ecdsa.PrivateKey

	BundleStream *cache.BundleStream

	SpiffeID string

	// How long to wait between expiry checks
	Interval time.Duration

	// Clk is the clock that the rotator will use to create a ticker
	Clk clock.Clock

	// Experimental API enabled
	ExperimentalAPIEnabled bool
}

func NewRotator(c *RotatorConfig) (Rotator, client.Client) {
	return newRotator(c)
}

func newRotator(c *RotatorConfig) (*rotator, client.Client) {
	if c.Interval == 0 {
		c.Interval = DefaultRotatorInterval
	}

	if c.Clk == nil {
		c.Clk = clock.New()
	}

	state := observer.NewProperty(State{
		SVID: c.SVID,
		Key:  c.SVIDKey,
	})

	rotMtx := new(sync.RWMutex)
	bsm := new(sync.RWMutex)

	cfg := &client.Config{
		TrustDomain: c.TrustDomain,
		Log:         c.Log,
		Addr:        c.ServerAddr,
		RotMtx:      rotMtx,
		KeysAndBundle: func() ([]*x509.Certificate, *ecdsa.PrivateKey, []*x509.Certificate) {
			s := state.Value().(State)

			bsm.RLock()
			bundles := c.BundleStream.Value()
			bsm.RUnlock()

			var rootCAs []*x509.Certificate
			if bundle := bundles[c.TrustDomain.String()]; bundle != nil {
				rootCAs = bundle.RootCAs()
			}
			return s.SVID, s.Key, rootCAs
		},
		ExperimentalAPIEnabled: c.ExperimentalAPIEnabled,
	}
	client := client.New(cfg)

	return &rotator{
		c:       c,
		client:  client,
		state:   state,
		clk:     c.Clk,
		backoff: backoff.NewBackoff(c.Clk, c.Interval),
		bsm:     bsm,
		rotMtx:  rotMtx,
	}, client
}
