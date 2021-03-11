package k8s_node

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/miekg/dns"
)

var log = clog.NewWithPlugin("k8s_node")

type K8SNode struct {
	Next        plugin.Handler
	Domain      string
	kubeconfig  string
	mutex       *sync.RWMutex
	hosts       *map[string]NodeAddr
}

type NodeAddr struct {
	Address net.IP
	Ipv6    bool
}



func (k K8SNode) AddARecord(msg *dns.Msg, state *request.Request, hosts map[string]NodeAddr, name string) bool {
	// Add A and AAAA record for name (if it exists) to msg.
	log.Info(name)
	answer, present := hosts[name]
	if present {
		// TODO: Make the ipv4/6 behavior RFC-compliant
		if answer.Ipv6 {
			aaaaheader := dns.RR_Header{Name: name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}
			msg.Answer = append(msg.Answer, &dns.AAAA{Hdr: aaaaheader, AAAA: answer.Address})
		} else {
			aheader := dns.RR_Header{Name: name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}
			msg.Answer = append(msg.Answer, &dns.A{Hdr: aheader, A: answer.Address})
		}
		return true
	}
	return false
}

func (k K8SNode) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {

	log.Debug("Received query")
	msg := new(dns.Msg)
	msg.SetReply(r)
	msg.Authoritative = true
	msg.RecursionAvailable = true
	state := request.Request{W: w, Req: r}
	log.Debugf("Looking for name: %s", state.QName())

	if !strings.HasSuffix(state.QName(), k.Domain+".") {
		log.Debugf("Skipping due to query '%s' not in our domain '%s'", state.QName(), k.Domain)
		return plugin.NextOrFailure(k.Name(), k.Next, ctx, w, r)
	}

	if state.QType() != dns.TypeA && state.QType() != dns.TypeAAAA {
		log.Debugf("Skipping due to unrecognized query type %v", state.QType())
		return plugin.NextOrFailure(k.Name(), k.Next, ctx, w, r)
	}

	msg.Answer = []dns.RR{}

	k.mutex.RLock()
	defer k.mutex.RUnlock()

	if k.AddARecord(msg, &state, *k.hosts, state.Name()) {
		log.Debug(msg)
		w.WriteMsg(msg)
		return dns.RcodeSuccess, nil
	}

	log.Debugf("No records found for '%s', forwarding to next plugin.", state.QName())
	return plugin.NextOrFailure(k.Name(), k.Next, ctx, w, r)
}

func (k *K8SNode) UpdateNodeList() {
	hosts := make(map[string]NodeAddr)

	// Get node list
	config, err := clientcmd.BuildConfigFromFlags("", k.kubeconfig)
	if err != nil {
		log.Errorf("Failed to build client config: %s", err)
		return
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Errorf("Failed to create client: %s", err)
		return
	}
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Errorf("Failed to get node list: %s", err)
		return
	}
	for _, n := range nodes.Items {
		name := ""
		addr := net.IP{}
		for _, a := range n.Status.Addresses {
			if a.Type == v1.NodeHostName {
				// Ensure that we always have an FQDN, even if the node name is not.
				name = strings.Split(a.Address, ".")[0] + "." + k.Domain + "."
			}
			if a.Type == v1.NodeInternalIP {
				addr = net.ParseIP(a.Address)
			}
		}
		ipv6 := true
		check := addr.To4()
		if check != nil {
			ipv6 = false
		}
		hosts[name] = NodeAddr{Address: addr, Ipv6: ipv6}
	}

	k.mutex.Lock()
	defer k.mutex.Unlock()
	// Clear maps so we don't have stale entries
	for h := range *k.hosts {
		delete(*k.hosts, h)
	}
	// Copy values into the shared maps only after we've collected all of them.
	// This prevents us from having to lock during the entire mdns browse time.
	for name, addr := range hosts {
		(*k.hosts)[name] = addr
	}
	log.Infof("hosts: %v", k.hosts)
	for name, entry := range *k.hosts {
		log.Debugf("%s: %v", name, entry)
	}
}

func (k K8SNode) Name() string { return "k8s_node" }

type ResponsePrinter struct {
	dns.ResponseWriter
}

func NewResponsePrinter(w dns.ResponseWriter) *ResponsePrinter {
	return &ResponsePrinter{ResponseWriter: w}
}

func (r *ResponsePrinter) WriteMsg(res *dns.Msg) error {
	fmt.Fprintln(out, k)
	return r.ResponseWriter.WriteMsg(res)
}

var out io.Writer = os.Stdout

const k = "k8s_node"
