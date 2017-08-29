package vault_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/vault/builtin/plugin"
	"github.com/hashicorp/vault/helper/pluginutil"
	vaulthttp "github.com/hashicorp/vault/http"
	"github.com/hashicorp/vault/logical"
	lplugin "github.com/hashicorp/vault/logical/plugin"
	"github.com/hashicorp/vault/logical/plugin/mock"
	"github.com/hashicorp/vault/vault"
)

func TestSystemBackend_Plugin_secret(t *testing.T) {
	cluster := testSystemBackendMock(t, 1, 1, logical.TypeLogical)
	// Seal the cluster
	ensureCoresSealed(t, cluster)

	barrierKeys := cluster.BarrierKeys

	for _, core := range cluster.Cores {
		for _, key := range barrierKeys {
			_, err := core.Unseal(vault.TestKeyCopy(key))
			if err != nil {
				t.Fatal(err)
			}
		}
		sealed, err := core.Sealed()
		if err != nil {
			t.Fatalf("err checking seal status: %s", err)
		}
		if sealed {
			t.Fatal("should not be sealed")
		}
		// Wait for active so post-unseal takes place
		// If it fails, it means unseal process failed
		vault.TestWaitActive(t, core.Core)
	}

	defer cluster.Cleanup()
}

func TestSystemBackend_Plugin_auth(t *testing.T) {
	cluster := testSystemBackendMock(t, 1, 1, logical.TypeCredential)

	// Seal the cluster
	ensureCoresSealed(t, cluster)

	barrierKeys := cluster.BarrierKeys

	for _, core := range cluster.Cores {
		for _, key := range barrierKeys {
			_, err := core.Unseal(vault.TestKeyCopy(key))
			if err != nil {
				t.Fatal(err)
			}
		}
		sealed, err := core.Sealed()
		if err != nil {
			t.Fatalf("err checking seal status: %s", err)
		}
		if sealed {
			t.Fatal("should not be sealed")
		}
		// Wait for active so post-unseal takes place
		// If it fails, it means unseal process failed
		vault.TestWaitActive(t, core.Core)
	}

	defer cluster.Cleanup()
}

func TestSystemBackend_Plugin_autoReload(t *testing.T) {
	cluster := testSystemBackendMock(t, 1, 1, logical.TypeLogical)
	defer cluster.Cleanup()

	core := cluster.Cores[0]

	// Update internal value
	req := logical.TestRequest(t, logical.UpdateOperation, "mock-0/internal")
	req.ClientToken = core.Client.Token()
	req.Data["value"] = "baz"
	resp, err := core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp != nil {
		t.Fatalf("bad: %v", resp)
	}

	// Call errors/rpc endpoint to trigger reload
	req = logical.TestRequest(t, logical.ReadOperation, "mock-0/errors/rpc")
	req.ClientToken = core.Client.Token()
	resp, err = core.HandleRequest(req)
	if err == nil {
		t.Fatalf("expected error from error/rpc request")
	}

	// Check internal value to make sure it's reset
	req = logical.TestRequest(t, logical.ReadOperation, "mock-0/internal")
	req.ClientToken = core.Client.Token()
	resp, err = core.HandleRequest(req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatalf("bad: response should not be nil")
	}
	if resp.Data["value"].(string) == "baz" {
		t.Fatal("did not expect backend internal value to be 'baz'")
	}
}

func TestSystemBackend_Plugin_SealUnseal(t *testing.T) {
	cluster := testSystemBackendMock(t, 1, logical.TypeLogical)
	defer func() {
		fmt.Println(" ===> Cleaning up test cluster...")
		cluster.Cleanup()
	}()

	//core := cluster.Cores[0]
	rootToken := cluster.RootToken
	keys := cluster.BarrierKeys

	for _, core := range cluster.Cores {
		fmt.Println(" ===> Sealing test cluster...")
		// Seal the cluster
		vault.TestWaitActive(t, core.Core)
		err := core.Core.Seal(rootToken)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, core := range cluster.Cores {
		fmt.Println(" ===> Unsealing test cluster...")
		// Unseal the cluster
		var err error
		var unsealed bool
		fmt.Println(len(keys))
		for _, key := range keys {
			if unsealed, err = core.Unseal(key); err != nil {
				t.Fatal(err)
			}
		}
		if !unsealed {
			t.Fatal("expected to be unsealed")
		}
	}
}

func TestSystemBackend_Plugin_reload(t *testing.T) {
	data := map[string]interface{}{
		"plugin": "mock-plugin",
	}
	t.Run("plugin", func(t *testing.T) { testSystemBackend_PluginReload(t, data) })

	data = map[string]interface{}{
		"mounts": "mock-0/,mock-1/",
	}
	t.Run("mounts", func(t *testing.T) { testSystemBackend_PluginReload(t, data) })
}

// Helper func to test different reload methods on plugin reload endpoint
func testSystemBackend_PluginReload(t *testing.T, reqData map[string]interface{}) {
	cluster := testSystemBackendMock(t, 1, 2, logical.TypeLogical)
	defer cluster.Cleanup()

	core := cluster.Cores[0]
	client := core.Client

	for i := 0; i < 2; i++ {
		// Update internal value in the backend
		resp, err := client.Logical().Write(fmt.Sprintf("mock-%d/internal", i), map[string]interface{}{
			"value": "baz",
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if resp != nil {
			t.Fatalf("bad: %v", resp)
		}
	}

	// Perform plugin reload
	resp, err := client.Logical().Write("sys/plugins/reload/backend", reqData)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp != nil {
		t.Fatalf("bad: %v", resp)
	}

	for i := 0; i < 2; i++ {
		// Ensure internal backed value is reset
		resp, err := client.Logical().Read(fmt.Sprintf("mock-%d/internal", i))
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if resp == nil {
			t.Fatalf("bad: response should not be nil")
		}
		if resp.Data["value"].(string) == "baz" {
			t.Fatal("did not expect backend internal value to be 'baz'")
		}
	}
}

// testSystemBackendMock returns a systemBackend with the desired number
// of mounted mock plugin backends
func testSystemBackendMock(t *testing.T, numCores, numMounts int, backendType logical.BackendType) *vault.TestCluster {
	coreConfig := &vault.CoreConfig{
		LogicalBackends: map[string]logical.Factory{
			"plugin": plugin.Factory,
		},
		CredentialBackends: map[string]logical.Factory{
			"plugin": plugin.Factory,
		},
	}

	cluster := vault.NewTestCluster(t, coreConfig, &vault.TestClusterOptions{
		HandlerFunc:        vaulthttp.Handler,
		KeepStandbysSealed: true,
		NumCores:           numCores,
	})
	cluster.Start()

	core := cluster.Cores[0]
	vault.TestWaitActive(t, core.Core)
	client := core.Client

	os.Setenv(pluginutil.PluginCACertPEMEnv, cluster.CACertPEMFile)

	switch backendType {
	case logical.TypeLogical:
		vault.TestAddTestPlugin(t, core.Core, "mock-plugin", "TestBackend_PluginMainLogical")
		for i := 0; i < numMounts; i++ {
			resp, err := client.Logical().Write(fmt.Sprintf("sys/mounts/mock-%d", i), map[string]interface{}{
				"type": "plugin",
				"config": map[string]interface{}{
					"plugin_name": "mock-plugin",
				},
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if resp != nil {
				t.Fatalf("bad: %v", resp)
			}
		}
	case logical.TypeCredential:
		vault.TestAddTestPlugin(t, core.Core, "mock-plugin", "TestBackend_PluginMainCredentials")
		for i := 0; i < numMounts; i++ {
			resp, err := client.Logical().Write(fmt.Sprintf("sys/auth/mock-%d", i), map[string]interface{}{
				"type":        "plugin",
				"plugin_name": "mock-plugin",
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if resp != nil {
				t.Fatalf("bad: %v", resp)
			}
		}
	default:
		t.Fatal("unknown backend type provided")
	}

	return cluster
}

func ensureCoresSealed(t *testing.T, c *vault.TestCluster) {
	for _, core := range c.Cores {
		if err := core.Shutdown(); err != nil {
			t.Fatal(err)
		}
		timeout := time.Now().Add(3 * time.Second)
		for {
			if time.Now().After(timeout) {
				t.Fatal("timeout waiting for core to seal")
			}
			sealed, err := core.Sealed()
			if err != nil {
				t.Fatal(err)
			}
			if sealed {
				break
			}
			time.Sleep(1 * time.Second)
		}
	}
}

func TestBackend_PluginMainLogical(t *testing.T) {
	if os.Getenv(pluginutil.PluginUnwrapTokenEnv) == "" {
		return
	}

	caPEM := os.Getenv(pluginutil.PluginCACertPEMEnv)
	if caPEM == "" {
		t.Fatal("CA cert not passed in")
	}

	factoryFunc := mock.FactoryType(logical.TypeLogical)

	args := []string{"--ca-cert=" + caPEM}

	apiClientMeta := &pluginutil.APIClientMeta{}
	flags := apiClientMeta.FlagSet()
	flags.Parse(args)
	tlsConfig := apiClientMeta.GetTLSConfig()
	tlsProviderFunc := pluginutil.VaultPluginTLSProvider(tlsConfig)
	err := lplugin.Serve(&lplugin.ServeOpts{
		BackendFactoryFunc: factoryFunc,
		TLSProviderFunc:    tlsProviderFunc,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackend_PluginMainCredentials(t *testing.T) {
	if os.Getenv(pluginutil.PluginUnwrapTokenEnv) == "" {
		return
	}

	caPEM := os.Getenv(pluginutil.PluginCACertPEMEnv)
	if caPEM == "" {
		t.Fatal("CA cert not passed in")
	}

	factoryFunc := mock.FactoryType(logical.TypeCredential)

	args := []string{"--ca-cert=" + caPEM}

	apiClientMeta := &pluginutil.APIClientMeta{}
	flags := apiClientMeta.FlagSet()
	flags.Parse(args)
	tlsConfig := apiClientMeta.GetTLSConfig()
	tlsProviderFunc := pluginutil.VaultPluginTLSProvider(tlsConfig)
	err := lplugin.Serve(&lplugin.ServeOpts{
		BackendFactoryFunc: factoryFunc,
		TLSProviderFunc:    tlsProviderFunc,
	})
	if err != nil {
		t.Fatal(err)
	}
}
