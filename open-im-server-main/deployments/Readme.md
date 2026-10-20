# Kubernetes 部署

> 说明：本项目日常开发与演示使用 standalone 单机模式（`mage start`），**不需要** Kubernetes。本文面向需要上 K8s 集群的场景，保留作为参考。

## 资源需求

- CPU：2 核
- 内存：4 GiB
- 磁盘：约 20 GiB（节点上）

## 前置条件

以下中间件需先部署完成：

- Redis
- MongoDB
- Kafka
- MinIO

## 部署步骤

### 1. 进入目标目录

```bash
cd ./deployments/deploy/
```

### 2. 修改配置（ConfigMap）

修改 `openim-config.yml`，需要改动以下几项：

**discovery.yml**

- `kubernetes.namespace`：默认 `default`，改为你实际使用的命名空间。

**mongodb.yml**

- `address`：已部署的 MongoDB 地址，或集群内的 Service 名 + 端口。
- `database`：数据库名（需提前创建）。
- `authSource`：认证库名。`authSource` 指用户凭证所属的数据库，用户必须在该库中创建。

**kafka.yml**

- `address`：已部署的 Kafka 地址，或 Service 名 + 端口。

**redis.yml**

- `address`：已部署的 Redis 地址，或 Service 名 + 端口。

**minio.yml**

- `internalAddress`：集群内 MinIO 的 Service 名 + 端口。
- `externalAddress`：对外暴露的 MinIO 地址。

### 3. 配置 Secret

Secret 用于存放密码、密钥这类少量敏感数据，用法与 ConfigMap 类似，但值必须是 base64 编码。

#### Redis

修改 `redis-secret.yml` 中的 `redis-password`：

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: openim-redis-secret
type: Opaque
data:
  redis-password: b3BlbklNMTIz # 改为你的 Redis 密码的 base64 编码；无密码可设为 ""
```

#### MongoDB

修改 `mongo-secret.yml` 中的 `mongo_openim_username` 与 `mongo_openim_password`：

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: openim-mongo-secret
type: Opaque
data:
  mongo_openim_username: b3BlbklN # 改为 Mongo 用户名的 base64 编码（该用户需创建在 authSource 库中）
  mongo_openim_password: b3BlbklNMTIz # 改为 Mongo 密码的 base64 编码；无密码可设为 ""
```

#### MinIO

修改 `minio-secret.yml` 中的 `minio-root-user` 与 `minio-root-password`：

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: openim-minio-secret
type: Opaque
data:
  minio-root-user: cm9vdA== # 改为 MinIO accessKeyID 的 base64 编码
  minio-root-password: b3BlbklNMTIz # 改为 MinIO secretAccessKey 的 base64 编码
```

#### Kafka

修改 `kafka-secret.yml` 中的 `kafka-password`：

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: openim-kafka-secret
type: Opaque
data:
  kafka-password: b3BlbklNMTIz # 改为 Kafka 密码的 base64 编码；无密码可设为 ""
```

### 4. 应用 Secret

```bash
kubectl apply -f redis-secret.yml -f minio-secret.yml -f mongo-secret.yml -f kafka-secret.yml
```

### 5. 应用配置

```bash
kubectl apply -f ./openim-config.yml
```

> 注意：若使用 `default` 命名空间，需要执行 `clusterRole.yml` 为 default ServiceAccount 创建集群角色绑定。
>
> 命名空间在 `openim-config.yml` 的 `discovery.yml` 中配置，改 `kubernetes.namespace` 即可。

```bash
kubectl apply -f ./clusterRole.yml
```

### 6. 部署各服务

> 注意：MinIO、Redis、Kafka 等基础服务必须先运行起来，再部署主应用。

```bash
kubectl apply \
  -f openim-api-deployment.yml \
  -f openim-api-service.yml \
  -f openim-msggateway-deployment.yml \
  -f openim-msggateway-service.yml \
  -f openim-msgtransfer-deployment.yml \
  -f openim-msgtransfer-service.yml \
  -f openim-push-deployment.yml \
  -f openim-push-service.yml \
  -f openim-rpc-auth-deployment.yml \
  -f openim-rpc-auth-service.yml \
  -f openim-rpc-user-deployment.yml \
  -f openim-rpc-user-service.yml \
  -f openim-rpc-friend-deployment.yml \
  -f openim-rpc-friend-service.yml \
  -f openim-rpc-group-deployment.yml \
  -f openim-rpc-group-service.yml \
  -f openim-rpc-conversation-deployment.yml \
  -f openim-rpc-conversation-service.yml \
  -f openim-rpc-msg-deployment.yml \
  -f openim-rpc-msg-service.yml
```

> 上游清单里还包含 `openim-crontask` 与 `openim-rpc-third`。本项目的 `openim-rpc-third`（对象存储独立服务）属于待移除模块，`openim-crontask` 也计划下线，因此上面的命令没有包含它们；如仍需要，可自行追加对应的 yml。

### 7. 验证

```bash
# 查看所有 Pod 状态
kubectl get pods

# 查看 Service
kubectl get svc

# 查看 Deployment
kubectl get deployments

# 查看全部资源
kubectl get all
```

### 8. 清理

```bash
kubectl delete -f ./
```

## 注意事项

- 如果使用自定义命名空间，记得给所有 `kubectl` 命令追加 `-n <namespace>`。
- 所有 Secret 的值都必须 base64 编码，可用 `echo -n 'your-password' | base64` 生成。
