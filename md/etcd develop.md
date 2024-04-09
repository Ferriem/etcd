# etcd develop

## 服务发现

了解集群中是否有进程在监听tcp或udp端口，通过名字查找和连接。

- 服务注册：同一service下的所有节点注册到相同目录下，节点启动后将自己的信息注册到所属服务的目录中。
- 健康检查：服务节点定时进行健康检查。注册到服务目录中的信息设置一个较短的TTL，运行正常的服务节点每隔一段时间去更新TTL，从而达到健康检查的效果。
- 服务发现：通过服务节点能查询到服务提供外部访问的IP和端口号。比如网关代理服务时能及时发现服务中新增节点，丢弃不可用节点。

### 服务注册和健康检查

启动一个服务的时候，将服务的地址写入etcd，注册服务，绑定lease，并以 `keep leases alive` 的方式检测服务是否正常运行。

```go
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

```

```go
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
```

## gRPC负载均衡

### 集中式

![image](https://img2020.cnblogs.com/blog/1508611/202005/1508611-20200518153536494-684598725.png)

### 客户端负载

![image](https://img2020.cnblogs.com/blog/1508611/202005/1508611-20200518155900462-1370526164.png)

服务提供方启动时，首先将服务地址注册到服务注册表，同时缓存并定期刷新目标服务地址列表，然后以某种负载均衡策略选择一个目标服务地址，最后向目标服务发起请求。LB和服务发现被分散到服务消费者内部，同时服务消费方和服务提供方时直接调用

gRPC已提供简单的负载均衡策略（如RR），实现提供的 `Builder` 和 `Resolver` 接口。

```go
type Builder interface {
  Build(target Target, cc ClientConn, opts BuildOptions) (Resolver, err)
  Scheme() string
}

type Resolver interface {
  ResolveNew(ResplveNowOption)
  Close()
}
```

对一个结构体自定义实现 `Build` `Scheme` `ResolveNew` 函数，可用`resolver.Register` 注册该变量

```go
r := etcdv3.NewServiceDiscovery(EtcdEndpoints)
resolver.Register(r)
```

负载均衡可通过grpc内置函数实现

```go
	const grpcServiceConfig = `{"loadBalancingPolicy": "round_robin"}`
	conn, err := grpc.NewClient(
		fmt.Sprintf("%s:///%s", r.Scheme(), SerName),
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
		grpc.WithInsecure(),
	)
```

### 自定义负载均衡

gRPC提供了 `PickerBuilder` 和 `Picker` 接口来实现自己的均衡负载策略。``

```go
type PickerBuilder interface {
	Build(info PickerBuildInfo) balancer.Picker
}

type Picker interface {
	Pick(info PickInfo) (PickResult, error)
}
```

需要添加服务器的负载权重，`resolver.Address`没有权重属性，将权重存储到地址的 `metadata`中。

```go
func SetAddrInfo(addr resolver.Address, addrInfo AddrInfo) resolver.Address {
	addr.Attributes = attributes.New(attributeKey{}, addrInfo)
	return addr
}
```

实现`build`接口返回选择器

```go
func (c *customPickerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	grpclog.Infof("customPicker: newPicker called with info: %v", info)
	if len(info.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}
	var scs []balancer.SubConn
	for subConn, addr := range info.ReadySCs {
		node := GetAddrInfo(addr.Address)
		if node.Weight < minWeight {
			node.Weight = minWeight
		} else if node.Weight > maxWeight {
			node.Weight = maxWeight
		}
		for i := 0; i < node.Weight; i++ {
			scs = append(scs, subConn)
		}
	}
	return &customPicker{
		subConns: scs,
	}
}
```

考虑子连接的选择

```go
func (p *customPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	p.mu.Lock()
	index := rand.Intn(len(p.subConns))
	sc := p.subConns[index]
	p.mu.Unlock()
	return balancer.PickResult{SubConn: sc}, nil
}
```

将该负载均衡命名，注册到gRPC负载均衡策略中

```go
const Name = "weight"

func newBuilder() balancer.Builder {
	return base.NewBalancerBuilder(Name, &customPickerBuilder{}, base.Config{HealthCheck: false})
}

func init() {
	balancer.Register(newBuilder())
}
```

服务端注册服务时携带权重，客户端使用该负载均衡策略。

## 分布式锁

- 排他性：

  ```go
  func Lock(key string, cli *clientv3.Client) error {
    resp, err := cli.Get(context.Background(), key)
    if err != nil {
      return err
    }
    if len(resp.Kvs) > 0 {
      return errors.New("lock failed")
    }
    _, err = cli.Put(context.Background(), key, "lock")
    if err != nil {
      return err
    }
    return nil
  }
  
  func Unlock(key string, cli *clientv3.Client) error {
    _, err := cli.Delete(context.Background(), key)
    return err
  }
  
  func waitDelete(key string, cli *clientv3.Client) {
    rch := cli.Watch(context.Background(), key) 
    for wresp := range rch {
      for _, ev := range wresp.Events {
        switch ev.Type {
        case mvccpb.DELETE:
         	return
        }
      }
    }
  }
  ```

- 容错性：etcd基于raft算法保证集群数据一致性

- 避免死锁：分布式锁一定能释放， 给key设置leases来避免死锁，如果上锁后操作比leases大

  - 操作没完成，锁被别人占用。
  - 操作完成后，把别人占用的锁解开。

- 给key设置lease后以`Keep lease alive`的方式延续leases，当client正常持有锁时，锁不会过期，当client崩溃后，程序不能续约，从而锁过期，避免死锁

  ```go
  func Lock(key string, cli *clientv3.Client) error {
    resp, err := cli.Get(context.Background(), key)
    if err != nil {
      return err
    }
    if len(resp.Kvs) > 0 {
      waitDelete(key, cli)
      return Lock(key, cli)
    }
    resp, err := cli.Grant(context.TODO(), 30)
    if err != nil {
      return err
    }
    _, err = cli.Put(context.Background(), key, "lock")
    if err != nil {
      return err
    }
    _, err = cli.KeepAlive(context.TODO(), resp.ID)
    if err != nil {
      return err
    }
    return nil
  }
  
  func Unlock(resp *clientv3.LeaseGrantResponse, cli *clientv3.Client) error {
    _, err := cli.Revoke(context.TODO(), resp.ID)
    return err
  }
  ```

### 官方分布式锁

```go
func ExampleMutex_Lock() {
  cli, err := clientv3.New(clientv3.Config{
    Endpoints: endpoints,
  })
  if err != nil {
    log.Fatal(err)
  }
  defer cli.Close()
  
  //create two separate sessions for lock competition
  s1, err := concurrency.NewSession(cli)
  if err != nil {
    log.Fatal(err)
  }
  defer s1.Close()
  m1 := concurrency.NewMutex(s1, "/my-lock/")
  
  s2, err := concurrency.NewSession(cli)
  if err != nil {
    log.Fatal(err)
  }
  defer s2.Close()
  m1 := concurrency.NewMutex(s2, "/my-lock/")
  
  //acquire lock for s1
  if err := m1.Lock(context.TODO(); err != nil) {
    log.Fatal(err)
  }
  fmt.Println("acquired lock for s1")
  
  m2Locked := make(chan struct{})
  go func() {
    defer close(m2.Locked)
    if err := m2.Lock(context.TODO()); err ! nil {
      log.Fatal(err)
    }
  }()
  
  if err := m1.Unlock(context.TODO(); err != nil) {
    log.Fatal(err)
  }
  fmt.Println("released lock for s1")
  <-m2Locked
  fmt.Println("acquired lock for s2")
}
```

### 事务

```go
Txn(context.TODO()).If(
  Compare(Value(k1), "<" v1),
  Compare(Version(k1), "=", 2)
).Then(
  OpPut(k2,v2), OpPut(k3,v3)
).Else(
  OpPut(k4,v4), OpPut(k5,v5)
).Commit()
```

```go
func ExampleKv_txn() {
  cli, err := clientv3.New(clientv3.Config{
    Endpoints: endpoints,
    DialTimeout: dial
  })
  if err != nil {
    log.Fatal(err)
  }
  defer cli.Close()
  
  kvc := clientv3.NewKV(cli)
  
  _,err := kvc.Put(context.TODO(), "key", "xyz")
  if err != nil {
    log.Fatal(err)
  }
  
  ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
  _, err := kvc.Txn(ctx).
  If(clientv3.Compare(clientv3.Value("key"), ">" "abc")).
  Then(clientv3.OpPut("key", "XYZ")).
  Else(clientv3.OpPut("key", "ABC")).
  Commit()
  cacel()
  if err != nil {
    log.Fatal(err)
  }
  gresp, err := kvc.Get(context.TODO(), "key")
  cancel()
  if err != nil {
    log.Fatal(err)
  }
  for _,ev :;= range gresp.Kvs {
    fmt.Println("%s : %s\n", ev.Key, ev.Value)
  }
}
```



