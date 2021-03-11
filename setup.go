package k8s_node

import (
	//"errors"
	//"fmt"
	//"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/coredns/caddy"
)

func init() {
	caddy.RegisterPlugin("k8s_node", caddy.Plugin{
		ServerType: "dns",
		Action:     setup,
	})
}

func setup(c *caddy.Controller) error {
	c.Next()
	c.NextArg()
	domain := c.Val()
	kubeconfig := "/var/lib/kubelet/kubeconfig"
	if c.NextArg() {
		kubeconfig = c.Val()
	}
	if c.NextArg() {
		return plugin.Error("k8s_node", c.ArgErr())
	}

	// Because the plugin interface uses a value receiver, we need to make these
	// pointers so all copies of the plugin point at the same maps.
	hosts := make(map[string]NodeAddr)
	mutex := sync.RWMutex{}
	k := K8SNode{Domain: strings.TrimSuffix(domain, "."), kubeconfig: kubeconfig, mutex: &mutex, hosts: &hosts}

	c.OnStartup(func() error {
		go nodeListLoop(&k)
		return nil
	})

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		k.Next = next
		return k
	})

	return nil
}

func nodeListLoop(k *K8SNode) {
	for {
		k.UpdateNodeList()
		time.Sleep(60 * time.Second)
	}
}
