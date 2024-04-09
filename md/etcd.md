# etcd

## Data model

- Storage methodologies

  etcd is designed to reliably store **infrequently updated data and provide reliable watch queries**. A persistent, multi-version, concurrency-control data model is a good fir for these use cases.

- Update in a new place

  etcd stores data in a multiversion persistent key-value store. The persistent key-value store preserves the previous version of a key-value pair when its value is superseded with new data. The key-value store is effetively immutable; its operations **do not update the structure in-place**, but instead always generate a new updated structure. All past versions of keys are still accessible and watchable after modification. To prevent the data store from growing indefinitely over time and from maintaining old versions, the store may be compacted to shed the oldest versions of superseded data.

- Revisions

  The key space maintains multiple revisions. When the store is created, the initial revision is 1. Each atomic mutative operation creates a new revision on the key space. All data held by previous revisions remains unchanged. Old versions of keys can still be accessed thorugh previous revisions. Likewise, **revisions are indexed as well**. If the store is compacted to save space, revisions **before compact revision will be removed**. Revisions are monotonically increasing over lifetime of a cluster.

- Physical model
  etcd stores the physical data as key-value pairs in a persistent **b+tree**. Each revision of the store's state only contains the delta from its previous revision to be efficient. A single revision may correspind to multiple keys in the tree.
  The key of key-value pair is a **3-tuple(major, sub, type)**. **Major is the store revisioni holding the key**. **Sub differentiates among keys within the same revision.**
  etcd also keeps a secondary in-memory **btree** index to speed up range queries over keys. The keys in the btree index are keys of the store exposes to user. The value if a pointer to the modification of the persistent b+tree. Compaction removes dead pointers.

![image](https://etcd.io/docs/v3.5/learning/img/data-model-figure-01.png)

## Client

- Introduction
  etcd server has proven its **robustness** with years of failure injection testing. Most complex application logic is already handled by etcd server and ites data stores.(e.g. cluster membership is transparent to client, with Raft-layer forwarding proposals to leader). Ideally, etcd server provides one logical cluster view of many physical machines, and client implements automatic failover between replicas.
- Glossary
  - Clientv3: etcd Official Go client for etcd v3 API
  - Balancer: etcd client load balancer that implements retry and failover mechanism, etcd client should automatically balance loads between multiple endpoints.
  - Endpoints: A list of etcd server endpoints that client can connect to. Typically, 3 or 5 client URLs of an etcd cluster.
  - Pinned endpoints: When configured with multiple endpointes, <= v3.3 client balancer chooses only one endpoint to establish, in order to conserve total open connections to etcd cluster. In v3.4, balancer round-robins pinned endpoints for every request, this distributing loads more evenly.
  - Client Connection: TCP connection that has been established to an etcd server, via gRPC Dial.
  - Sub Connection: gRPC SubConn interface. Each sub-connection contains a list of addresses. Balancer creates a SubConn from a list of resolved addresses. gRPC ClientConn can map to multiple SubConn. etcd v3.4 balancer employs internal resolver to establish one sub-connection for each endpoint.

- Requirements

  - Correntness: global ordering properties, never write corrupted data, at-most once semantics for mutable operations, watch never observes partial events, and so on.
  - Liveness: Ideally, clients detect unavailable servers with HTTP/2 ping and failover to other nodes with clear error messages.
  - Effectiveness: previous TCP connections should be [gracefully closed](https://github.com/etcd-io/etcd/issues/9212) after endpoint switch. Failover mechanism should effectively predict the next replica to connect, without wastefully retrying on failed nodes.
  - Portability: Since etcd is fully committed to gRPC, implementation should be closely aligned with gRPC long-term design goals.

- Overview

  - balancer that establishes gRPC connections to an etcd cluster,
  - API client that sends RPCs to an etcd server
  - error hander that decides whether to retry a failed request or switch endpoints.

- Clientv3-grpc1.23

  Rather than maintaining a list of unhealthy endpoints, which may be stale, simply round robin to the next endpoint whenever client gets disconnected from, the current endpoint.
  Internally, when given multiple endpoints, clientv3-grpc1.23 creates multiple **sub-connections**, while clientv3-grpc1.7 creates only one connection to a pinned endpoint. For instance, in 5-node cluster, clientv3-grpc1.23 balancer would require 5 TCP connections, while clientv3-grpc1.7 only requires one. By preserving the pool of TCP connections, clientv3-grpc1.23 may consume more resources but provide more flexible load balancer with better failover performance.

  ![image](https://etcd.io/docs/v3.5/learning/img/client-balancer-figure-09.png)

## Learner

- Challenges

  - New Cliuster member overloads Leader

    Leader sends large snapshot to be overloaded. Which may block heartbeat sends.

  - Network Partitions

    - Leader isolation

      Reverts back to follower which will affect the cluster availability.

    - Cluster Split 3+1

      Leader still maintain the active quorum, no election happens.

    - Cluster Split 2+2

      Leadership election happends.

  - Cluster Misconfigurations

    ![image](https://etcd.io/docs/v3.5/learning/img/server-learner-figure-08.png)

    A simple misconfiguration can fail the whole cluster into an inoperative state. In such case, an operator need manually recreate the cluster with `etcd --force-new-clusrer` flag.

- Raft Learner

  [Raft §4.2.1](https://github.com/ongardie/dissertation/blob/master/stanford.pdf) introduces a new node state “Learner”, which joins the cluster as a **non-voting member** until it catches up to leader’s logs.

  An operator should do the minimum amount of work possible to add a new learner node. `member add --learner` command to add a new learner, which joins cluster as a **non-voting member but still receives all data** from leader.

  When a learning has caught up with leader's progress, the leader can be promoted to a voting member using `member promote` API, which then counts towards the quorum.

  etcd server validates promote request to ensure te operational safety.

## Authentication design

- Functionality requirements
  - Per connection authentication, not per request
    - User ID + password based on authentication implemented for the gRPC API
    - Authentication must be refreshed after auth policy changes
  - Its functionality should be as simple and useful as v2
    - v3 provides a flat key space, unlike the directory struture of v2. Permission checking will be provided as interval matching.

- Require change

  - A client must create a dedicated connection only for authentication before sending authenticated requests.
  - Add permission information to the Raft commands(`etcdserverpv.InternalRaftRequest`)
  - Every request is permission checked in the state machine layer, rather than API layer.

- **Design** 

  - Authentication

    At first, a client must create a gRPC connection only to authenticate its user ID and password. An etcd server will repond witth an authentication reply. The response will be **authentication token** on sucess or an error on failure. The client can use its authentication token to present its credentials to tecd when making API requests.

    The client connect used to request the authentication token is typically thrown away; it cannot carry the new token's credentials. This is because gRPC doesn't provide a way for adding per RPC credential after creation of the connection(calling `grpc.Dial()`). Therefore, a client cannot assign a token to its connection that is obtained through the connection. The client needs a new connection for using token.

    `Authenticate()` RPC generates an authentication token based on a given user name and password. Performing the check in the state machine apply phase would cause performance trouble.
    For good performance, the v3 auth mechanism checks passwords in **etcd's API layer**, where it can be parallelized outside of raft. However, this can lead to potential time-of-check

    - Client A sends a request `Authenticate()`
    - the API layer processes the password checking part of `Authenticate()`
    - another client B sends a request of `ChangePassword()` and the server completes it.
    - the same state machine layer processes the part of getting a revision number for the `Authenticatte()` from A
    - the server returns a success to A
    - now A is authenticated on an obsolete password.

    For avoiding such a situation, the API layer performs version number validation based on the revision number of the auth store. During the password checking, the API layer **saves the revision number** of auth store. **After successful password checking, the API layer compares the saved revision number of the latest revision number**.

    After authenticating with `Authenticate()`, a client can create a gRPC connection as it would without auth. The client must associate the token with the newly created connection. `grpc.WithPerRPCCredentials()` provides the functionality for this purpose.

    The auth info in `etcdserverpb.RequestHeader` is checked in the apply phase of the state machine. This step checks the user is granted permission to requested keys on the latest revision of auth store.

  - Two type tokens

    - simple
    - JWT: stateless, its token can include metadata including username and revision, so servers don't need to remember correspondence between tokens and the metadata.

## API

### gRPC Services

Every API request sent to an etcd server is a gRPC remote procedure call. RPCs in tecd are categorized based on functionality into services.

Services important for dealing with etcd's key space include:

- KV - Creates, updates, fetches, and deletes key-value pairs.
- Watch - Monitors changes to keys.
- Lease- Primitives for consuming client keep-alive messages.

Services which manage the cluster itself include:

- Auth - Role based authentication mechanism for authenticating users
- Cluster - Provides membership information and configuration facilities.
- Maintenance  - Takes recovery snapshots, defragments to teh store and returens per-member status information.

### Requests and Responses

Each RPC has a function `Name` which takes `NameRequest` as an argument and returns `NameRepsonse` as a response.

```protobuf
service KV {
  Range(RangeRequest) returns (RangeResponse)
}

message ResponseHeader {
  uint64 cluster_id = 1;
  uint64 member_id = 2;
  int64 revision = 3;
  uint64 raft_term = 4;
}
```

An application may read the `Cluster_ID` or `Member_ID` field to ensure it is communicating with the intended cluster.

Applications can use `Raft_Term` to detect when the cluster completes a new leader election.

### Key-Value API

A key-value pair is the smallest unit that the key-value API can minipulate. Each key-value pair has a number of fields, defined in [protobuf format](https://github.com/etcd-io/etcd/blob/master/api/mvccpb/kv.proto):

```protobuf
message KeyValue {
  // key is the key in bytes. An empty key is not allowed.
  bytes key = 1;
  // create_revision is the revision of last creation on this key.
  int64 create_revision = 2;
  // mod_revision is the revision of last modification on this key.
  int64 mod_revision = 3;
  // version is the version of the key. A deletion resets
  // the version to zero and any modification of the key
  // increases its version.
  int64 version = 4;
  // value is the value held by the key, in bytes.
  bytes value = 5;
  // lease is the ID of the lease that attached to key.
  // When the attached lease expires, the key will be deleted.
  // If lease is 0, then no lease is attached to the key.
  int64 lease = 6;
}
```

- Revision

  The revision serves as a global logical clock, sequentially ordering all updates to the store. The change represented by a new revision is incremental. **A new revision means writing the changes to the backend's B+tree, keyed by the incremented revision.**

  Revisions become more valuable when considering etcd's **multi-version concurrency control** backend. The MVCC model means that the key-value store can be viewed from past revisions since historical ket revisions are retained.

- Key ranges

  The etcd data model indexes all kets over a flat binary key space. Instead of listing keys by directory, keys are listed by key intervals `[a,b]`.

  These intervals are often referred to as "ranges" in etcd. Operations over ranges are more powerful than operations on directories.If `range_end` is `key` plus one (e.g., “aa”+1 == “ab”, “a\xff”+1 == “b”), then the range represents all keys prefixed with key. If both `key` and `range_end` are ‘\0’, then range represents all keys. If `range_end` is ‘\0’, the range is all keys greater than or equal to the key argument.

- Range

  Keys are fetched from the key-value store using the `Range` API, which takes a `RangeRequest`:

  ```protobuf
  message RangeRequest {
    enum SortOrder {
  	NONE = 0; // default, no sorting
  	ASCEND = 1; // lowest target value first
  	DESCEND = 2; // highest target value first
    }
    enum SortTarget {
  	KEY = 0;
  	VERSION = 1;
  	CREATE = 2;
  	MOD = 3;
  	VALUE = 4;
    }
  
    bytes key = 1;
    bytes range_end = 2;
    int64 limit = 3;
    int64 revision = 4;
    SortOrder sort_order = 5;
    SortTarget sort_target = 6;
    bool serializable = 7;
    bool keys_only = 8;
    bool count_only = 9;
    int64 min_mod_revision = 10;
    int64 max_mod_revision = 11;
    int64 min_create_revision = 12;
    int64 max_create_revision = 13;
  }
  
  message RangeResponse {
    ResponseHeader header = 1;
    repeated mvccpb.KeyValue kvs = 2;
    bool more = 3;
    int64 count = 4;
  }
  ```

- Put

  ```protobuf
  message PutRequest {
    bytes key = 1;
    bytes value = 2;
    int64 lease = 3;
    bool prev_kv = 4;
    bool ignore_value = 5;
    bool ignore_lease = 6;
  }
  
  message PutResponse {
    ResponseHeader header = 1;
    mvccpb.KeyValue prev_kv = 2;
  }
  ```

- Delete Range

  ```protobuf
  message DeleteRangeRequest {
    bytes key = 1;
    bytes range_end = 2;
    bool prev_kv = 3;
  }
  
  message DeleteRangeResponse {
    ResponseHeader header = 1;
    int64 deleted = 2;
    repeated mvccpb.KeyValue prev_kvs = 3;
  }
  ```

- Transaction

  A transaction can atomically process multiple requests in a single request. For modifications to the key-value store, this means the store’s revision is incremented only once for the transaction and all events generated by the transaction will have the same revision. However, modifications to the same key multiple times within a single transaction are forbidden.

  All transactions are guarded by a conjunction of comparisons. Compare with a given value, or check a key's revision or version. Two different comparisons may apply to the same or different keys.

  Each comparison is encoded as a `Compare` message:

  ```protobuf
  message Compare {
    enum CompareResult {
      EQUAL = 0;
      GREATER = 1;
      LESS = 2;
      NOT_EQUAL = 3;
    }
    enum CompareTarget {
      VERSION = 0;
      CREATE = 1;
      MOD = 2;
      VALUE= 3;
    }
    CompareResult result = 1;
    // target is the key-value field to inspect for the comparison.
    CompareTarget target = 2;
    // key is the subject key for the comparison operation.
    bytes key = 3;
    oneof target_union {
      int64 version = 4;
      int64 create_revision = 5;
      int64 mod_revision = 6;
      bytes value = 7;
    }
  }
  ```

  After processing the comparison block, the transaction applies a block of requests. A block is a list of `RequestOp` messages:

  ```protobuf
  message RequestOp {
    // request is a union of request types accepted by a transaction.
    oneof request {
      RangeRequest request_range = 1;
      PutRequest request_put = 2;
      DeleteRangeRequest request_delete_range = 3;
    }
  }
  ```

  All together, a transaction is issued with a `Txn` API call, which takes a `TxnRequest`

  ```protobuf
  message TxnRequest {
    repeated Compare compare = 1;
    repeated RequestOp success = 2;
    repeated RequestOp failure = 3;
  }
  
  message TxnResponse {
    ResponseHeader header = 1;
    bool succeeded = 2;
    repeated ResponseOp responses = 3;
  }
  
  message ResponseOp {
    oneof response {
      RangeResponse response_range = 1;
      PutResponse response_put = 2;
      DeleteRangeResponse response_delete_range = 3;
    }
  }
  ```

### Watch API

- Events

  ```protobuf
  message Event {
    enum EventType {
      PUT = 0;
      DELETE = 1;
    }
    EventType type = 1;
    KeyValue kv = 2;
    KeyValue prev_kv = 3;
  }
  ```

- Watch stream

  ```protobuf
  message WatchCreateRequest {
    bytes key = 1;
    bytes range_end = 2;
    int64 start_revision = 3;
    bool progress_notify = 4;
  
    enum FilterType {
      NOPUT = 0;
      NODELETE = 1;
    }
    repeated FilterType filters = 5;
    bool prev_kv = 6;
  }
  
  message WatchResponse {
    ResponseHeader header = 1;
    int64 watch_id = 2;
    bool created = 3;
    bool canceled = 4;
    int64 compact_revision = 5;
  
    repeated mvccpb.Event events = 11;
  }
  
  message WatchCancelRequest {
     int64 watch_id = 1;
  }
  ```

### Lease API

- Obtain leases

  ```protobuf
  message LeaseGrantRequest {
    int64 TTL = 1;
    int64 ID = 2;
  }
  
  message LeaseGrantResponse {
    ResponseHeader header = 1;
    int64 ID = 2;
    int64 TTL = 3;
  }
  
  message LeaseRevokeRequest {
    int64 ID = 1;
  }
  ```

- Keep alive

  ```protobuf
  message LeaseKeepAliveRequest {
    int64 ID = 1;
  }
  
  message LeaseKeepAliveResponse {
    ResponseHeader header = 1;
    int64 ID = 2;
    int64 TTL = 3;
  }
  ```

  