package commit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"text/template"

	securejoin "github.com/cyphar/filepath-securejoin"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/argoproj/argo-cd/v2/commitserver/apiclient"
)

// WriteForPaths writes the manifests, hydrator.metadata, and README.md files for each path in the provided paths. It
// also writes a root-level hydrator.metadata file containing the repo URL and dry SHA.
func WriteForPaths(rootPath string, repoUrl string, drySha string, paths []*apiclient.PathDetails) error {
	// Write the top-level readme.
	err := writeMetadata(rootPath, hydratorMetadataFile{DrySHA: drySha, RepoURL: repoUrl})
	if err != nil {
		return fmt.Errorf("failed to write top-level hydrator metadata: %w", err)
	}

	for _, p := range paths {
		hydratePath := p.Path
		if hydratePath == "." {
			hydratePath = ""
		}
		var fullHydratePath string
		fullHydratePath, err = securejoin.SecureJoin(rootPath, hydratePath)
		if err != nil {
			return fmt.Errorf("failed to construct hydrate path: %w", err)
		}
		// TODO: consider switching to securejoin.MkdirAll: https://github.com/cyphar/filepath-securejoin?tab=readme-ov-file#mkdirall
		err = os.MkdirAll(fullHydratePath, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create path: %w", err)
		}

		// Write the manifests
		err = writeManifests(fullHydratePath, p.Manifests)
		if err != nil {
			return fmt.Errorf("failed to write manifests: %w", err)
		}

		// Write hydrator.metadata containing information about the hydration process.
		hydratorMetadata := hydratorMetadataFile{
			Commands: p.Commands,
			DrySHA:   drySha,
			RepoURL:  repoUrl,
		}
		err = writeMetadata(fullHydratePath, hydratorMetadata)
		if err != nil {
			return fmt.Errorf("failed to write hydrator metadata: %w", err)
		}

		// Write README
		err = writeReadme(fullHydratePath, hydratorMetadata)
		if err != nil {
			return fmt.Errorf("failed to write readme: %w", err)
		}
	}
	return nil
}

// writeMetadata writes the metadata to the hydrator.metadata file.
func writeMetadata(dirPath string, metadata hydratorMetadataFile) error {
	hydratorMetadataJson, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal hydrator metadata: %w", err)
	}
	// No need to use SecureJoin here, as the path is already sanitized.
	hydratorMetadataPath := path.Join(dirPath, "hydrator.metadata")
	err = os.WriteFile(hydratorMetadataPath, hydratorMetadataJson, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to write hydrator metadata: %w", err)
	}
	return nil
}

// writeReadme writes the readme to the README.md file.
func writeReadme(dirPath string, metadata hydratorMetadataFile) error {
	readmeTemplate := template.New("readme")
	readmeTemplate, err := readmeTemplate.Parse(manifestHydrationReadmeTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse readme template: %w", err)
	}
	// Create writer to template into
	// No need to use SecureJoin here, as the path is already sanitized.
	readmePath := path.Join(dirPath, "README.md")
	readmeFile, err := os.Create(readmePath)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to create README file: %w", err)
	}
	err = readmeTemplate.Execute(readmeFile, metadata)
	closeErr := readmeFile.Close()
	if closeErr != nil {
		log.WithError(closeErr).Error("failed to close README file")
	}
	if err != nil {
		return fmt.Errorf("failed to execute readme template: %w", err)
	}
	return nil
}

// writeManifests writes the manifests to the manifest.yaml file, truncating the file if it exists and appending the
// manifests in the order they are provided.
func writeManifests(dirPath string, manifests []*apiclient.ManifestDetails) error {
	// If the file exists, truncate it.
	// No need to use SecureJoin here, as the path is already sanitized.
	manifestPath := path.Join(dirPath, "manifest.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		err = os.Truncate(manifestPath, 0)
		if err != nil {
			return fmt.Errorf("failed to empty manifest file: %w", err)
		}
	}

	file, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to open manifest file: %w", err)
	}
	defer func() {
		err := file.Close()
		if err != nil {
			log.WithError(err).Error("failed to close file")
		}
	}()
	for _, m := range manifests {
		obj := &unstructured.Unstructured{}
		err = json.Unmarshal([]byte(m.Manifest), obj)
		if err != nil {
			return fmt.Errorf("failed to unmarshal manifest: %w", err)
		}
		// Marshal the manifests
		buf := bytes.Buffer{}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		err = enc.Encode(&obj.Object)
		if err != nil {
			return fmt.Errorf("failed to encode manifest: %w", err)
		}
		mYaml := buf.Bytes()
		mYaml = append(mYaml, []byte("\n---\n\n")...)
		// Write the yaml to manifest.yaml
		_, err = file.Write(mYaml)
		if err != nil {
			return fmt.Errorf("failed to write manifest: %w", err)
		}
	}
	return nil
}
