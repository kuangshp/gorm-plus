package generator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyMapperPathResolution(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module mapperfixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, test := range []struct {
		name       string
		cfg        Config
		api, proto string
	}{
		{"dto", Config{MapperPath: "mapper"}, "mapper", ""},
		{"api", Config{MapperPath: "mapper", ApiPath: "desc"}, "mapper", ""},
		{"proto", Config{MapperPath: "mapper", ProtoPath: "proto"}, "", "mapper/entityproto"},
		{"api and proto", Config{MapperPath: "mapper", ProtoPath: "proto", ApiPath: "desc"}, "mapper/apiproto", "mapper/entityproto"},
		{"explicit dto", Config{MapperPath: "mapper", APIMapperPath: "custom/api"}, "custom/api", ""},
		{"explicit api", Config{MapperPath: "mapper", ProtoPath: "proto", ApiPath: "desc", APIMapperPath: "custom/api"}, "custom/api", "mapper/entityproto"},
		{"explicit proto", Config{MapperPath: "mapper", ProtoPath: "proto", ApiPath: "desc", ProtoMapperPath: "custom/proto"}, "mapper/apiproto", "custom/proto"},
		{"explicit both", Config{MapperPath: "mapper", ProtoPath: "proto", ApiPath: "desc", APIMapperPath: "custom/api", ProtoMapperPath: "custom/proto"}, "custom/api", "custom/proto"},
		{"no legacy field", Config{ProtoPath: "proto", ApiPath: "desc"}, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.cfg
			if err := resolveConfigPaths(&cfg); err != nil {
				t.Fatal(err)
			}
			absolute := func(path string) string {
				if path == "" {
					return ""
				}
				return filepath.Join(root, path)
			}
			if cfg.APIMapperPath != absolute(test.api) || cfg.ProtoMapperPath != absolute(test.proto) {
				t.Fatalf("mapper outputs: api=%q, proto=%q; want api=%q, proto=%q", cfg.APIMapperPath, cfg.ProtoMapperPath, absolute(test.api), absolute(test.proto))
			}
		})
	}
}

func TestLoadLegacyMapperYAML(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module mapperfixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	path := filepath.Join(root, "generator.yaml")
	if err := os.WriteFile(path, []byte("mapper_path: query/mapper\nproto_path: proto\napi_path: desc\napi_mapper_path: custom/api\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MapperPath != "query/mapper" {
		t.Fatalf("legacy YAML field lost: %q", cfg.MapperPath)
	}
	if err := resolveConfigPaths(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.APIMapperPath != filepath.Join(root, "custom/api") || cfg.ProtoMapperPath != filepath.Join(root, "query/mapper/entityproto") {
		t.Fatalf("legacy YAML outputs: %+v", cfg)
	}
}
