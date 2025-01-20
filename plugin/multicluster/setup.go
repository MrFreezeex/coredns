package multicluster

import (
	"context"
	"errors"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/kubernetes"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const pluginName = "multicluster"

// init registers this plugin.
func init() { plugin.Register(pluginName, setup) }

func setup(c *caddy.Controller) error {
	// Do not call klog.InitFlags(nil) here.  It will cause reload to panic.
	klog.SetLogger(logr.New(&kubernetes.LoggerAdapter{P: log}))

	multiCluster, err := ParseStanza(c)
	if err != nil {
		return plugin.Error(pluginName, err)
	}

	onStart, onShut, err := multiCluster.InitController(context.Background())
	if err != nil {
		return plugin.Error(pluginName, err)
	}
	if onStart != nil {
		c.OnStartup(onStart)
	}
	if onShut != nil {
		c.OnShutdown(onShut)
	}

	// Add the Plugin to CoreDNS, so Servers can use it in their plugin chain.
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		multiCluster.Next = next
		return multiCluster
	})

	c.OnStartup(func() error {
		m := dnsserver.GetConfig(c).Handler("kubernetes")
		if m == nil {
			return plugin.Error(pluginName, errors.New("kubernetes plugin not loaded"))
		}

		x := m.(kubernetes.Kubernetes) // if found this must be ok
		multiCluster.nsAddrsFunc = x.NsAddrsPlugin
		return nil
	})

	return nil
}

// ParseStanza parses a kubernetes stanza
func ParseStanza(c *caddy.Controller) (*MultiCluster, error) {
	c.Next() // Skip "multicluster" label

	opts := controllerOpts{
		initEndpointsCache: true, // watch endpoints by default
	}

	zones := plugin.OriginsFromArgsOrServerBlock(c.RemainingArgs(), c.ServerBlockKeys)
	multiCluster := New(zones)
	multiCluster.opts = opts

	for c.NextBlock() {
		switch c.Val() {
		case "kubeconfig":
			args := c.RemainingArgs()
			if len(args) != 1 && len(args) != 2 {
				return nil, c.ArgErr()
			}
			overrides := &clientcmd.ConfigOverrides{}
			if len(args) == 2 {
				overrides.CurrentContext = args[1]
			}
			config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
				&clientcmd.ClientConfigLoadingRules{ExplicitPath: args[0]},
				overrides,
			)
			multiCluster.ClientConfig = config
		case "fallthrough":
			multiCluster.Fall.SetZonesFromArgs(c.RemainingArgs())
		case "noendpoints":
			if len(c.RemainingArgs()) != 0 {
				return nil, c.ArgErr()
			}
			multiCluster.opts.initEndpointsCache = false
		default:
			return nil, c.Errf("unknown property '%s'", c.Val())
		}
	}

	return multiCluster, nil
}
