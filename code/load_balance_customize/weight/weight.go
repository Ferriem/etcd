package weight

import (
	"math/rand"
	"sync"

	"google.golang.org/grpc/attributes"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/resolver"
)

const Name = "weight"

var (
	minWeight = 1
	maxWeight = 5
)

type attributeKey struct{}

// will be stored inside Address metadata in order to use weighted balancer
type AddrInfo struct {
	Weight int
}

func SetAddrInfo(addr resolver.Address, addrInfo AddrInfo) resolver.Address {
	addr.Attributes = attributes.New(attributeKey{}, addrInfo)
	return addr
}

func GetAddrInfo(addr resolver.Address) AddrInfo {
	v := addr.Attributes.Value(attributeKey{})
	ai, _ := v.(AddrInfo)
	return ai
}

func newBuilder() balancer.Builder {
	return base.NewBalancerBuilder(Name, &customPickerBuilder{}, base.Config{HealthCheck: false})
}

func init() {
	balancer.Register(newBuilder())
}

type customPickerBuilder struct {
}

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

type customPicker struct {
	subConns []balancer.SubConn
	mu       sync.Mutex
}

func (p *customPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	p.mu.Lock()
	index := rand.Intn(len(p.subConns))
	sc := p.subConns[index]
	p.mu.Unlock()
	return balancer.PickResult{SubConn: sc}, nil
}
