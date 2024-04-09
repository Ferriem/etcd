package main

import (
	"context"
	"log"
	"sync"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type ServiceDiscovery struct {
	cli        *clientv3.Client
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

func (s *ServiceDiscovery) WatchService(prefix string) error {
	resp, err := s.cli.Get(context.Background(), prefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}

	for _, kv := range resp.Kvs {
		s.SetServiceList(string(kv.Key), string(kv.Value))
	}

	go s.watcher(prefix)
	return nil
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
	log.Println("set key :", key, "val:", val)
}

func (s *ServiceDiscovery) DelServiceList(key string) {
	s.serverList.Delete(key)
	log.Println("del key:", key)
}

func (s *ServiceDiscovery) GetServices() []string {
	addrs := make([]string, 0)
	s.serverList.Range(func(key, value interface{}) bool {
		addrs = append(addrs, value.(string))
		return true
	})
	return addrs
}

func (s *ServiceDiscovery) Close() error {
	return s.cli.Close()
}

func main() {
	var enpoints = []string{"localhost:2379"}
	srv := NewServiceDiscovery(enpoints)
	defer srv.Close()
	srv.WatchService("/web/")
	srv.WatchService("/gRPC/")
	for {
		select {
		case <-time.Tick(10 * time.Second):
			log.Println(srv.GetServices())
		}
	}
}
