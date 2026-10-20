package discovery

import (
	"os"
)

func setupTestEnvironment() {
	os.Setenv("ZOOKEEPER_SCHEMA", "openim")
	os.Setenv("ZOOKEEPER_ADDRESS", "172.28.0.1")
	os.Setenv("ZOOKEEPER_PORT", "12181")
	os.Setenv("ZOOKEEPER_USERNAME", "")
	os.Setenv("ZOOKEEPER_PASSWORD", "")
}

//func TestNewDiscoveryRegister(t *testing.T) {
//	setupTestEnvironment()
//	conf := config.NewGlobalConfig()
//	tests := []struct {
//		envType        string
//		gatewayName    string
//		expectedError  bool
//		expectedResult bool
//	}{
//		{"zookeeper", "MessageGateway", false, true},
//		{"k8s", "MessageGateway", false, true},
//		{"direct", "MessageGateway", false, true},
//		{"invalid", "MessageGateway", true, false},
//	}
//
//	for _, test := range tests {
//		conf.Envs.Discovery = test.envType
//		conf.RpcRegisterName.OpenImMessageGatewayName = test.gatewayName
//		client, err := NewDiscoveryRegister(conf)
//
//		if test.expectedError {
//			assert.Error(t, err)
//		} else {
//			assert.NoError(t, err)
//			if test.expectedResult {
//				assert.Implements(t, (*discovery.SvcDiscoveryRegistry)(nil), client)
//			} else {
//				assert.Nil(t, client)
//			}
//		}
//	}
//}
