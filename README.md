# Garage Kubernetes Operator

A Kubernetes operator for managing [Garage](https://garagehq.deuxfleurs.fr/) - a distributed S3-compatible object storage system designed for self-hosting.

## Description

This operator automates the deployment and lifecycle management of Garage clusters on Kubernetes. It provides:

- **GarageCluster**: Deploy and manage Garage StatefulSets with all configuration options
- **GarageBucket**: Create and manage S3 buckets with quotas and website hosting
- **GarageKey**: Manage S3 access keys with fine-grained bucket permissions
- **GarageNode**: Configure node layouts for capacity and zone assignments
- **GarageAdminToken**: Manage admin API authentication tokens

### Key Features

- Full Garage configuration exposed via CRDs
- Multi-cluster federation support for geo-distributed deployments
- Automatic node layout management
- Website hosting configuration
- Webhook validation for configuration errors
- Rich status reporting and observability

## Getting Started

### Local Development

```bash
# Set up kind cluster with CRDs and operator
make dev-up

# Apply test resources (GarageCluster, GarageBucket, GarageKey)
make dev-test

# View status of all garage resources
make dev-status

# Stream operator logs
make dev-logs

# Rebuild and reload operator after code changes
make dev-load

# Tear down the cluster
make dev-down
```

### Deploy to Production Cluster

**Build and push the operator image:**

```bash
make docker-build docker-push IMG=<registry>/garage-operator:tag
```

**Install CRDs:**

```bash
make install
```

**Deploy the operator:**

```bash
make deploy IMG=<registry>/garage-operator:tag
```

**Apply sample resources:**

```bash
kubectl apply -k config/samples/
```

### Uninstall

```bash
kubectl delete -k config/samples/
make uninstall
make undeploy
```

## Quick Start Example

```yaml
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageCluster
metadata:
  name: garage
spec:
  replicas: 3
  zone: us-east-1
  replication:
    factor: 3
  storage:
    dataSize: 100Gi
  network:
    rpcBindPort: 3901
---
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageBucket
metadata:
  name: my-bucket
spec:
  clusterRef:
    name: garage
  quotas:
    maxSize: 10Gi
---
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageKey
metadata:
  name: my-key
spec:
  clusterRef:
    name: garage
  bucketPermissions:
    - bucketRef: my-bucket
      read: true
      write: true
```

## Multi-Cluster Federation

Garage supports geo-distributed deployments across multiple Kubernetes clusters. Key requirements:

- **Direct connectivity**: Each node must reach all other nodes on RPC port (3901)
- **Shared RPC secret**: All nodes authenticate with the same 32-byte hex secret
- **Zone labels**: Each cluster uses a unique zone for fault tolerance

Example federation setup:

```yaml
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageCluster
metadata:
  name: garage
spec:
  replicas: 3
  zone: us-east-1
  replication:
    factor: 3
    zoneRedundancy: "AtLeast(2)"
  publicEndpoint:
    type: LoadBalancer
    loadBalancer:
      perNode: true
  remoteClusters:
    - name: garage-eu
      zone: eu-west-1
      autoDiscover: true
      connection:
        kubeConfigSecretRef:
          name: eu-kubeconfig
          key: config
```

## License

Copyright 2026 Raj Singh.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
