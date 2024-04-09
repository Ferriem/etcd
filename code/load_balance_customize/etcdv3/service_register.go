package etcdv3

import (
	"context"
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
	weight        string
}

func NewServiceRegister(endpoints []string, addr, weight string, lease int64) (*ServiceRegister, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints, //[]string{"host:port"...}
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	srv := &ServiceRegister{
		cli:    cli,
		key:    "/" + schema + "/" + addr,
		weight: weight,
	}

	log.Printf("%s", srv.key)

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
	_, err = s.cli.Put(context.Background(), s.key, s.weight, clientv3.WithLease(resp.ID))
	//设置续租，定期发送需求请求
	leaseRespChan, err := s.cli.KeepAlive(context.Background(), resp.ID)
	if err != nil {
		return err
	}
	s.leaseID = resp.ID
	log.Println(s.leaseID)
	s.keepAliveChan = leaseRespChan

	go func() {
		for {
			<-s.keepAliveChan
		}
	}()

	log.Printf("put key:%s weight:%s with lease:%d success\n", s.key, s.weight, s.leaseID)
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
