package main

import (
	"context"
	"fmt"
	"log"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// ServiceRegister 创建租约注册服务
type ServiceRegister struct {
	cli           *clientv3.Client
	leaseID       clientv3.LeaseID
	keepAliveChan <-chan *clientv3.LeaseKeepAliveResponse
	key           string
	value         string
}

func NewServiceRegister(endpoints []string, key, val string, lease int64) (*ServiceRegister, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints, //[]string{"host:port"...}
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}

	srv := &ServiceRegister{
		cli:   cli,
		key:   key,
		value: val,
	}

	if err := srv.putKeyWithLease(lease); err != nil {
		return nil, err
	}

	return srv, nil
}

func (s *ServiceRegister) putKeyWithLease(lease int64) error {
	// 创建一个租约
	resp, err := s.cli.Grant(context.Background(), lease)
	if err != nil {
		return err
	}
	//注册服务绑定租约
	_, err = s.cli.Put(context.Background(), s.key, s.value, clientv3.WithLease(resp.ID))
	//设置续租，定期发送需求请求
	leaseRespChan, err := s.cli.KeepAlive(context.Background(), resp.ID)
	if err != nil {
		return err
	}
	s.leaseID = resp.ID
	log.Println(s.leaseID)
	s.keepAliveChan = leaseRespChan
	log.Printf("put key:%s value:%s with lease:%d success\n", s.key, s.value, s.leaseID)
	return nil
}

// ListenLeaseRespChan 监听自动续租应答
func (s *ServiceRegister) ListenLeaseRespChan() {
	for leaseKeepResp := range s.keepAliveChan {
		log.Println("续租成功", leaseKeepResp.ID)
	}
	log.Println("关闭续租")
}

// Close 注销服务
func (s *ServiceRegister) Close() error {
	if _, err := s.cli.Revoke(context.Background(), s.leaseID); err != nil {
		return err
	}
	log.Println("关闭租约")
	return s.cli.Close()
}

func main() {
	var endpoints = []string{"localhost:2379"}
	srv, err := NewServiceRegister(endpoints, "/web/node1", "localhost:8000", 5)
	if err != nil {
		fmt.Println(err)
	}
	go srv.ListenLeaseRespChan()
	select {
	// case <-time.After(10 * time.Second):
	// 	srv.Close()
	}
}
