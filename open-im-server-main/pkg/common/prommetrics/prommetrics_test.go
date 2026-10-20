package prommetrics

import "testing"

//func TestNewGrpcPromObj(t *testing.T) {
//	// 构造一个自定义指标，传给 NewGrpcPromObj 函数。
//	customMetric := prometheus.NewCounter(prometheus.CounterOpts{
//		Name: "test_metric",
//		Help: "This is a test metric.",
//	})
//	cusMetrics := []prometheus.Collector{customMetric}
//
//	// 用这些自定义指标调用 NewGrpcPromObj。
//	reg, grpcMetrics, err := NewGrpcPromObj(cusMetrics)
//
//	// 断言没有返回错误。
//	assert.NoError(t, err)
//
//	// 断言 registry 已正确初始化。
//	assert.NotNil(t, reg)
//
//	// 断言 grpcMetrics 已正确初始化。
//	assert.NotNil(t, grpcMetrics)
//
//	// 断言自定义指标已注册。
//	mfs, err := reg.Gather()
//	assert.NoError(t, err)
//	assert.NotEmpty(t, mfs) // Ensure some metrics are present.
//	found := false
//	for _, mf := range mfs {
//		if *mf.Name == "test_metric" {
//			found = true
//			break
//		}
//	}
//	assert.True(t, found, "Custom metric not found in registry")
//}

//func TestGetGrpcCusMetrics(t *testing.T) {
//	conf := config2.NewGlobalConfig()
//
//	config2.InitConfig(conf, "../../config")
//	// 针对 GetGrpcCusMetrics 中的 switch 分支测试各种情况。
//	testCases := []struct {
//		name     string
//		expected int // 每种情况期望的指标数量。
//	}{
//		{conf.RpcRegisterName.OpenImMessageGatewayName, 1},
//	}
//
//	for _, tc := range testCases {
//		t.Run(tc.name, func(t *testing.T) {
//			metrics := GetGrpcCusMetrics(tc.name, &conf.RpcRegisterName)
//			assert.Len(t, metrics, tc.expected)
//		})
//	}
//}

func TestName(t *testing.T) {
	RegistryApi()
	RegistryApi()

}
