package etcdv3

import (
	"context"
	"log"
	"sync"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/resolver"
)

const schema = "grpclb"

type ServiceDiscovery struct {
	cli        *clientv3.Client
	cc         resolver.ClientConn
	serverList sync.Map
}

func NewServiceDiscovery(endpoints []string) *ServiceDiscovery {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	return &ServiceDiscovery{
		cli: cli,
	}
}

// Build creates a new resolver for the given target
func (s *ServiceDiscovery) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	log.Println("Build")
	s.cc = cc
	prefix := "/" + target.URL.Scheme + "/" + target.Endpoint() + "/"
	resp, err := s.cli.Get(context.Background(), prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	for _, ev := range resp.Kvs {
		s.SetServiceList(string(ev.Key), string(ev.Value))
	}
	s.cc.UpdateState(resolver.State{Addresses: s.getServices()})
	go s.watcher(prefix)
	return s, nil
}

func (s *ServiceDiscovery) ResolveNow(rn resolver.ResolveNowOptions) {
	log.Println("ResolveNow")
}

func (s *ServiceDiscovery) Scheme() string {
	return schema
}

func (s *ServiceDiscovery) watcher(prefix string) {
	rch := s.cli.Watch(context.Background(), prefix, clientv3.WithPrefix())
	log.Printf("watching prefix:%s now...", prefix)
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch ev.Type {
			case mvccpb.PUT:
				s.SetServiceList(string(ev.Kv.Key), string(ev.Kv.Value))
			case mvccpb.DELETE:
				s.DelServiceList(string(ev.Kv.Key))
			}
		}
	}
}

func (s *ServiceDiscovery) SetServiceList(key, val string) {
	s.serverList.Store(key, val)
	s.cc.UpdateState(resolver.State{Addresses: s.getServices()})
	log.Println("set key :", key, "val:", val)
}

func (s *ServiceDiscovery) DelServiceList(key string) {
	s.serverList.Delete(key)
	s.cc.UpdateState(resolver.State{Addresses: s.getServices()})
	log.Println("del key:", key)
}

func (s *ServiceDiscovery) getServices() []resolver.Address {
	addrs := make([]resolver.Address, 0)
	s.serverList.Range(func(key, value interface{}) bool {
		addrs = append(addrs, resolver.Address{Addr: value.(string)})
		return true
	})
	return addrs
}

func (s *ServiceDiscovery) Close() {
	log.Println("close etcd client")
	s.cli.Close()
}
