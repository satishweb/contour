// Copyright Project Contour Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package featuretests

import (
	"testing"

	"github.com/projectcontour/contour/internal/contour"

	envoy_api_v2_auth "github.com/envoyproxy/go-control-plane/envoy/api/v2/auth"
	envoy_api_v2_listener "github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	projcontour "github.com/projectcontour/contour/apis/projectcontour/v1"
	"github.com/projectcontour/contour/internal/dag"
	"github.com/projectcontour/contour/internal/envoy"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestNonTLSListener(t *testing.T) {
	rh, c, done := setup(t)
	defer done()

	// assert that without any ingress objects registered
	// there are no active listeners
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "0",
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "0",
	})

	// i1 is a simple ingress, no hostname, no tls.
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: "backend",
				ServicePort: intstr.FromInt(80),
			},
		},
	}

	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add it and assert that we now have a ingress_http listener
	rh.OnAdd(i1)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:          "ingress_http",
				Address:       envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains:  envoy.FilterChains(envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0)),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// i2 is the same as i1 but has the kubernetes.io/ingress.allow-http: "false" annotation
	i2 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
			Annotations: map[string]string{
				"kubernetes.io/ingress.allow-http": "false",
			},
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: "backend",
				ServicePort: intstr.FromInt(80),
			},
		},
	}

	// update i1 to i2 and verify that ingress_http has gone.
	rh.OnUpdate(i1, i2)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// i3 is similar to i2, but uses the ingress.kubernetes.io/force-ssl-redirect: "true" annotation
	// to force 80 -> 443 upgrade
	i3 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
			Annotations: map[string]string{
				"ingress.kubernetes.io/force-ssl-redirect": "true",
			},
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: "backend",
				ServicePort: intstr.FromInt(80),
			},
		},
	}

	// update i2 to i3 and check that ingress_http has returned
	rh.OnUpdate(i2, i3)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:          "ingress_http",
				Address:       envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains:  envoy.FilterChains(envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0)),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}

func TestTLSListener(t *testing.T) {
	rh, c, done := setup(t)
	defer done()

	// s1 is a tls secret
	s1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	// i1 is a tls ingress
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			TLS: []v1beta1.IngressTLS{{
				Hosts:      []string{"kuard.example.com"},
				SecretName: "secret",
			}},
			Rules: []v1beta1.IngressRule{{
				Host: "kuard.example.com",
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{{
							Backend: v1beta1.IngressBackend{
								ServiceName: "backend",
								ServicePort: intstr.FromInt(80),
							},
						}},
					},
				},
			}},
		},
	}

	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add secret
	rh.OnAdd(s1)

	// assert that there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// add ingress and assert the existence of ingress_http and ingres_https
	rh.OnAdd(i1)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:          "ingress_http",
				Address:       envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains:  envoy.FilterChains(envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0)),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			&v2.Listener{
				Name:    "ingress_https",
				Address: envoy.SocketAddress("0.0.0.0", 8443),
				ListenerFilters: envoy.ListenerFilters(
					envoy.TLSInspector(),
				),
				FilterChains: []*envoy_api_v2_listener.FilterChain{
					filterchaintls("kuard.example.com", s1,
						httpsFilterFor("kuard.example.com"),
						nil, "h2", "http/1.1"),
				},
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// i2 is the same as i1 but has the kubernetes.io/ingress.allow-http: "false" annotation
	i2 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
			Annotations: map[string]string{
				"kubernetes.io/ingress.allow-http": "false",
			},
		},
		Spec: v1beta1.IngressSpec{
			TLS: []v1beta1.IngressTLS{{
				Hosts:      []string{"kuard.example.com"},
				SecretName: "secret",
			}},
			Rules: []v1beta1.IngressRule{{
				Host: "kuard.example.com",
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{{
							Backend: v1beta1.IngressBackend{
								ServiceName: "backend",
								ServicePort: intstr.FromInt(80),
							},
						}},
					},
				},
			}},
		},
	}

	// update i1 to i2 and verify that ingress_http has gone.
	rh.OnUpdate(i1, i2)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_https",
				Address: envoy.SocketAddress("0.0.0.0", 8443),
				ListenerFilters: envoy.ListenerFilters(
					envoy.TLSInspector(),
				),
				FilterChains: []*envoy_api_v2_listener.FilterChain{
					filterchaintls("kuard.example.com", s1,
						httpsFilterFor("kuard.example.com"),
						nil, "h2", "http/1.1"),
				},
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// delete secret and assert that ingress_https is removed
	rh.OnDelete(s1)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}

func TestHTTPProxyTLSListener(t *testing.T) {
	rh, c, done := setup(t)
	defer done()

	// secret1 is a tls secret
	secret1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	svc1 := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: secret1.Namespace,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	}

	// p1 is a tls httpproxy
	p1 := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: secret1.Namespace,
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "kuard.example.com",
				TLS: &projcontour.TLS{
					SecretName:             secret1.Name,
					MinimumProtocolVersion: "1.1",
				},
			},
			Routes: []projcontour.Route{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},
				Services: []projcontour.Service{{
					Name: svc1.Name,
					Port: int(svc1.Spec.Ports[0].Port),
				}},
			}},
		},
	}

	// p2 is a tls httpproxy
	p2 := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: secret1.Namespace,
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "kuard.example.com",
				TLS: &projcontour.TLS{
					SecretName:             secret1.Name,
					MinimumProtocolVersion: "1.3",
				},
			},
			Routes: []projcontour.Route{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},
				Services: []projcontour.Service{{
					Name: svc1.Name,
					Port: int(svc1.Spec.Ports[0].Port),
				}},
			}},
		},
	}

	// add secret
	rh.OnAdd(secret1)

	// assert that there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	l1 := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			filterchaintls("kuard.example.com", secret1,
				httpsFilterFor("kuard.example.com"),
				nil, "h2", "http/1.1"),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}

	// add service
	rh.OnAdd(svc1)

	// add ingress and assert the existence of ingress_http and ingres_https
	rh.OnAdd(p1)

	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains: envoy.FilterChains(
					envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
				),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			l1,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// delete secret and assert both listeners are removed because the
	// httpproxy is no longer valid.
	rh.OnDelete(secret1)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	rh.OnDelete(p1)
	// add secret
	rh.OnAdd(secret1)
	l2 := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			envoy.FilterChainTLS(
				"kuard.example.com",
				envoy.DownstreamTLSContext(
					&dag.Secret{Object: secret1},
					envoy_api_v2_auth.TlsParameters_TLSv1_3,
					nil,
					"h2", "http/1.1"),
				envoy.Filters(httpsFilterFor("kuard.example.com")),
			),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}

	// add ingress and assert the existence of ingress_http and ingres_https
	rh.OnAdd(p2)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains: envoy.FilterChains(
					envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
				),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			l2,
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}

func TestLDSFilter(t *testing.T) {
	rh, c, done := setup(t)
	defer done()

	// s1 is a tls secret
	s1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	// i1 is a tls ingress
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			TLS: []v1beta1.IngressTLS{{
				Hosts:      []string{"kuard.example.com"},
				SecretName: "secret",
			}},
			Rules: []v1beta1.IngressRule{{
				Host: "kuard.example.com",
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{{
							Backend: v1beta1.IngressBackend{
								ServiceName: "backend",
								ServicePort: intstr.FromInt(80),
							},
						}},
					},
				},
			}},
		},
	}

	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add secret
	rh.OnAdd(s1)

	// add ingress and fetch ingress_https
	rh.OnAdd(i1)
	c.Request(listenerType, "ingress_https").Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_https",
				Address: envoy.SocketAddress("0.0.0.0", 8443),
				ListenerFilters: envoy.ListenerFilters(
					envoy.TLSInspector(),
				),
				FilterChains: []*envoy_api_v2_listener.FilterChain{
					filterchaintls("kuard.example.com", s1,
						httpsFilterFor("kuard.example.com"),
						nil, "h2", "http/1.1"),
				},
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
		),
		TypeUrl: listenerType,
	})

	// fetch ingress_http
	c.Request(listenerType, "ingress_http").Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains: envoy.FilterChains(
					envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
				),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
		),
		TypeUrl: listenerType,
	})

	// fetch something non existent.
	c.Request(listenerType, "HTTP").Equals(&v2.DiscoveryResponse{
		TypeUrl: listenerType,
	})
}

func TestLDSStreamEmpty(t *testing.T) {
	_, c, done := setup(t)
	defer done()

	// assert that streaming LDS with no ingresses does not stall.
	c.Request(listenerType, "HTTP").Equals(&v2.DiscoveryResponse{
		VersionInfo: "0",
		TypeUrl:     listenerType,
		Nonce:       "0",
	})
}

func TestLDSIngressHTTPUseProxyProtocol(t *testing.T) {
	rh, c, done := setup(t, func(conf *contour.ListenerConfig) {
		conf.UseProxyProto = true
	})
	defer done()

	// assert that without any ingress objects registered
	// there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "0",
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "0",
	})

	// i1 is a simple ingress, no hostname, no tls.
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: "backend",
				ServicePort: intstr.FromInt(80),
			},
		},
	}
	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add it and assert that we now have a ingress_http listener using
	// the proxy protocol (the true param to filterchain)
	rh.OnAdd(i1)
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "1",
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				ListenerFilters: envoy.ListenerFilters(
					envoy.ProxyProtocol(),
				),
				FilterChains:  envoy.FilterChains(envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0)),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "1",
	})
}

func TestLDSIngressHTTPSUseProxyProtocol(t *testing.T) {
	rh, c, done := setup(t, func(conf *contour.ListenerConfig) {
		conf.UseProxyProto = true
	})
	defer done()

	// s1 is a tls secret
	s1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	// i1 is a tls ingress
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			TLS: []v1beta1.IngressTLS{{
				Hosts:      []string{"kuard.example.com"},
				SecretName: "secret",
			}},
			Rules: []v1beta1.IngressRule{{
				Host: "kuard.example.com",
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{{
							Backend: v1beta1.IngressBackend{
								ServiceName: "backend",
								ServicePort: intstr.FromInt(80),
							},
						}},
					},
				},
			}},
		},
	}

	// add secret
	rh.OnAdd(s1)

	// assert that there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add ingress and assert the existence of ingress_http and ingres_https and both
	// are using proxy protocol
	rh.OnAdd(i1)

	ingress_https := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.ProxyProtocol(),
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			filterchaintls("kuard.example.com", s1,
				httpsFilterFor("kuard.example.com"),
				nil, "h2", "http/1.1"),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				ListenerFilters: envoy.ListenerFilters(
					envoy.ProxyProtocol(),
				),
				FilterChains:  envoy.FilterChains(envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0)),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			ingress_https,
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}

func TestLDSCustomAddressAndPort(t *testing.T) {
	rh, c, done := setup(t, func(conf *contour.ListenerConfig) {
		conf.HTTPAddress = "127.0.0.100"
		conf.HTTPPort = 9100
		conf.HTTPSAddress = "127.0.0.200"
		conf.HTTPSPort = 9200
	})
	defer done()

	// s1 is a tls secret
	s1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	// i1 is a tls ingress
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			TLS: []v1beta1.IngressTLS{{
				Hosts:      []string{"kuard.example.com"},
				SecretName: "secret",
			}},
			Rules: []v1beta1.IngressRule{{
				Host: "kuard.example.com",
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{{
							Backend: v1beta1.IngressBackend{
								ServiceName: "backend",
								ServicePort: intstr.FromInt(80),
							},
						}},
					},
				},
			}},
		},
	}

	// add secret
	rh.OnAdd(s1)

	// assert that there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "0",
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "0",
	})

	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add ingress and assert the existence of ingress_http and ingres_https and both
	// are using proxy protocol
	rh.OnAdd(i1)

	ingress_http := &v2.Listener{
		Name:    "ingress_http",
		Address: envoy.SocketAddress("127.0.0.100", 9100),
		FilterChains: envoy.FilterChains(
			envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
		),
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}
	ingress_https := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("127.0.0.200", 9200),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			filterchaintls("kuard.example.com", s1,
				httpsFilterFor("kuard.example.com"),
				nil, "h2", "http/1.1"),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			ingress_http,
			ingress_https,
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}

func TestLDSCustomAccessLogPaths(t *testing.T) {
	rh, c, done := setup(t, func(conf *contour.ListenerConfig) {
		conf.HTTPAccessLog = "/tmp/http_access.log"
		conf.HTTPSAccessLog = "/tmp/https_access.log"
	})
	defer done()

	// s1 is a tls secret
	s1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	// i1 is a tls ingress
	i1 := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			TLS: []v1beta1.IngressTLS{{
				Hosts:      []string{"kuard.example.com"},
				SecretName: "secret",
			}},
			Rules: []v1beta1.IngressRule{{
				Host: "kuard.example.com",
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{{
							Backend: v1beta1.IngressBackend{
								ServiceName: "backend",
								ServicePort: intstr.FromInt(80),
							},
						}},
					},
				},
			}},
		},
	}

	rh.OnAdd(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	})

	// add secret
	rh.OnAdd(s1)

	// assert that there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "0",
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "0",
	})

	rh.OnAdd(i1)

	ingress_http := &v2.Listener{
		Name:    "ingress_http",
		Address: envoy.SocketAddress("0.0.0.0", 8080),
		FilterChains: envoy.FilterChains(
			envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/tmp/http_access.log"), 0),
		),
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}
	ingress_https := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			filterchaintls("kuard.example.com", s1,
				envoy.HTTPConnectionManagerBuilder().
					AddFilter(envoy.FilterMisdirectedRequests("kuard.example.com")).
					DefaultFilters().
					RouteConfigName("https/kuard.example.com").
					MetricsPrefix(contour.ENVOY_HTTPS_LISTENER).
					AccessLoggers(envoy.FileAccessLogEnvoy("/tmp/https_access.log")).
					Get(),
				nil, "h2", "http/1.1"),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "1",
		Resources: resources(t,
			ingress_http,
			ingress_https,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "1",
	})
}

func TestHTTPProxyHTTPS(t *testing.T) {
	rh, c, done := setup(t)
	defer done()

	// assert that there is only a static listener
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "0",
		Resources: resources(t,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "0",
	})

	// s1 is a tls secret
	s1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}

	// p1 is a httpproxy that has TLS
	p1 := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "example.com",
				TLS: &projcontour.TLS{
					SecretName: "secret",
				},
			},
			Routes: []projcontour.Route{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},
				Services: []projcontour.Service{{
					Name: "kuard",
					Port: 8080,
				}},
			}},
		},
	}

	svc1 := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kuard",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     8080,
			}},
		},
	}

	// add secret
	rh.OnAdd(s1)

	// add service
	rh.OnAdd(svc1)

	// add httpproxy
	rh.OnAdd(p1)

	ingressHTTP := &v2.Listener{
		Name:    "ingress_http",
		Address: envoy.SocketAddress("0.0.0.0", 8080),
		FilterChains: envoy.FilterChains(
			envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
		),
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}

	ingressHTTPS := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			filterchaintls("example.com", s1,
				httpsFilterFor("example.com"),
				nil, "h2", "http/1.1"),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		VersionInfo: "1",
		Resources: resources(t,
			ingressHTTP,
			ingressHTTPS,
			staticListener(),
		),
		TypeUrl: listenerType,
		Nonce:   "1",
	})
}

func TestHTTPProxyMinimumTLSVersion(t *testing.T) {
	rh, c, done := setup(t, func(conf *contour.ListenerConfig) {
		conf.MinimumTLSVersion = envoy_api_v2_auth.TlsParameters_TLSv1_2
	})

	defer done()

	// secret1 is a tls secret
	secret1 := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Type: "kubernetes.io/tls",
		Data: secretdata(CERTIFICATE, RSA_PRIVATE_KEY),
	}
	rh.OnAdd(secret1)

	svc1 := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend",
			Namespace: "default",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	}
	rh.OnAdd(svc1)

	// p1 is a tls httpproxy
	p1 := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "kuard.example.com",
				TLS: &projcontour.TLS{
					SecretName:             "secret",
					MinimumProtocolVersion: "1.1",
				},
			},
			Routes: []projcontour.Route{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},
				Services: []projcontour.Service{{
					Name: "backend",
					Port: 80,
				}},
			}},
		},
	}
	rh.OnAdd(p1)

	l1 := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			envoy.FilterChainTLS(
				"kuard.example.com",
				envoy.DownstreamTLSContext(
					&dag.Secret{Object: secret1},
					envoy_api_v2_auth.TlsParameters_TLSv1_2,
					nil,
					"h2", "http/1.1"),
				envoy.Filters(httpsFilterFor("kuard.example.com")),
			),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}

	// verify that p1's TLS 1.1 minimum has been upgraded to 1.2
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains: envoy.FilterChains(
					envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
				),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			l1,
			staticListener(),
		),
		TypeUrl: listenerType,
	})

	// p2 is a tls httpproxy
	p2 := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "simple",
			Namespace: "default",
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "kuard.example.com",
				TLS: &projcontour.TLS{
					SecretName:             "secret",
					MinimumProtocolVersion: "1.3",
				},
			},
			Routes: []projcontour.Route{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},

				Services: []projcontour.Service{{
					Name: "backend",
					Port: 80,
				}},
			}},
		},
	}
	rh.OnUpdate(p1, p2)

	l2 := &v2.Listener{
		Name:    "ingress_https",
		Address: envoy.SocketAddress("0.0.0.0", 8443),
		ListenerFilters: envoy.ListenerFilters(
			envoy.TLSInspector(),
		),
		FilterChains: []*envoy_api_v2_listener.FilterChain{
			envoy.FilterChainTLS(
				"kuard.example.com",
				envoy.DownstreamTLSContext(
					&dag.Secret{Object: secret1},
					envoy_api_v2_auth.TlsParameters_TLSv1_3,
					nil,
					"h2", "http/1.1"),
				envoy.Filters(httpsFilterFor("kuard.example.com")),
			),
		},
		SocketOptions: envoy.TCPKeepaliveSocketOptions(),
	}

	// verify that p2's TLS 1.3 minimum has NOT been downgraded to 1.2
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains: envoy.FilterChains(
					envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
				),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			l2,
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}

func TestLDSHTTPProxyRootCannotDelegateToAnotherRoot(t *testing.T) {
	rh, c, done := setup(t)
	defer done()

	svc1 := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "green",
			Namespace: "marketing",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name:     "http",
				Protocol: "TCP",
				Port:     80,
			}},
		},
	}
	rh.OnAdd(svc1)

	child := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "blog",
			Namespace: "marketing",
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "www.containersteve.com",
			},
			Routes: []projcontour.Route{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},
				Services: []projcontour.Service{{
					Name: svc1.Name,
					Port: 80,
				}},
			}},
		},
	}
	rh.OnAdd(child)

	root := &projcontour.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "root-blog",
			Namespace: "default",
		},
		Spec: projcontour.HTTPProxySpec{
			VirtualHost: &projcontour.VirtualHost{
				Fqdn: "blog.containersteve.com",
			},
			Includes: []projcontour.Include{{
				Conditions: []projcontour.MatchCondition{{
					Prefix: "/",
				}},
				Name:      child.Name,
				Namespace: child.Namespace,
			}},
		},
	}
	rh.OnAdd(root)

	// verify that port 80 is present because while it is not possible to
	// delegate to it, child can host a vhost which opens port 80.
	c.Request(listenerType).Equals(&v2.DiscoveryResponse{
		Resources: resources(t,
			&v2.Listener{
				Name:    "ingress_http",
				Address: envoy.SocketAddress("0.0.0.0", 8080),
				FilterChains: envoy.FilterChains(
					envoy.HTTPConnectionManager("ingress_http", envoy.FileAccessLogEnvoy("/dev/stdout"), 0),
				),
				SocketOptions: envoy.TCPKeepaliveSocketOptions(),
			},
			staticListener(),
		),
		TypeUrl: listenerType,
	})
}
