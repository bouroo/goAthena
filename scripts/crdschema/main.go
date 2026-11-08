// Command crdschema extracts one CRD's openAPIV3Schema from a multi-document
// YAML manifest and writes it as JSON, so a schema validator can check
// custom resources against the API's own definition.
//
// The manifests it reads are the Helm-rendered CRD sets (Agones ships
// install/yaml/install.yaml). A Helm template is not valid YAML on its own,
// so the input is the rendered form.
//
// Usage: go run ./scripts/crdschema <manifest.yaml> <kind> <out.json>
//
// The kind is matched against the CRD's spec.names.kind (e.g. Fleet).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// crd is the part of a CustomResourceDefinition this tool reads.
type crd struct {
	Kind string `yaml:"kind"`
	Spec struct {
		Names struct {
			Kind string `yaml:"kind"`
		} `yaml:"names"`
		Versions []struct {
			Name   string `yaml:"name"`
			Schema struct {
				OpenAPIV3Schema map[string]any `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: crdschema <manifest.yaml> <kind> <out.json>")
		os.Exit(2)
	}
	manifestPath, wantKind, outPath := os.Args[1], os.Args[2], os.Args[3]

	// #nosec G703 -- the paths are the operator's own, supplied on the
	// command line to a build-time tool that runs on their machine.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		fail("read %s: %v", manifestPath, err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	for {
		var doc crd
		err := dec.Decode(&doc)
		if err != nil {
			break
		}
		if doc.Kind != "CustomResourceDefinition" || doc.Spec.Names.Kind != wantKind {
			continue
		}
		for _, v := range doc.Spec.Versions {
			if v.Schema.OpenAPIV3Schema == nil {
				continue
			}
			out, err := json.Marshal(v.Schema.OpenAPIV3Schema)
			if err != nil {
				fail("marshal schema: %v", err)
			}
			// #nosec G703 -- as above: the operator's own path.
			if err := os.WriteFile(outPath, out, 0o600); err != nil {
				fail("write %s: %v", outPath, err)
			}
			fmt.Printf("%s %s schema -> %s\n", wantKind, v.Name, outPath)
			return
		}
	}
	fail("no CustomResourceDefinition for kind %q in %s", wantKind, manifestPath)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
