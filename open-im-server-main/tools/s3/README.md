# S3 切换存储引擎后，迁移数据

- 编译
```shell
go build -o s3convert main.go
```

- 启动
```shell
./s3convert -config <配置目录路径> -name <旧的 s3 名称>
# ./s3convert -config ./../../config -name minio
```
