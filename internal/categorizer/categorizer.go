// Package categorizer provides project-level category detection.
//
// It uses file-based heuristics to classify a project directory
// into categories like "backend", "frontend", "ops", etc.
// This is the domain service for project categorization.
package categorizer

import (
	"os"
	"path/filepath"
)

// DetectProjectCategory inspects a project directory and returns
// a category string based on the files present.
//
// Returns "" if the category cannot be determined.
//
// Heuristic rules (checked in order):
//  1. Dockerfile/terraform/k8s manifests → "ops"
//  2. package.json + tsconfig → "frontend" (unless also has go.mod/pom.xml → "fullstack")
//  3. go.mod/Cargo.toml/pom.xml/build.gradle → "backend"
//  4. pyproject.toml + notebooks → "data"
//  5. ios/android manifests → "mobile"
//  6. Only docs (mkdocs.yml, docusaurus) → "docs"
func DetectProjectCategory(projectPath string) string {
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(projectPath, name))
		return err == nil
	}

	hasBackend := has("go.mod") || has("Cargo.toml") || has("pom.xml") || has("build.gradle") || has("build.gradle.kts") || has("requirements.txt") || has("Gemfile")
	hasFrontend := has("package.json") && (has("tsconfig.json") || has("next.config.js") || has("next.config.mjs") || has("vite.config.ts") || has("vite.config.js") || has("angular.json") || has("vue.config.js"))
	hasOps := has("Dockerfile") || has("docker-compose.yml") || has("docker-compose.yaml") || has("terraform") || has("Pulumi.yaml") || has("helm") || has("k8s") || has("kubernetes") || has(".github/workflows")
	hasData := has("pyproject.toml") && (has("notebooks") || has("jupyter") || has("dbt_project.yml"))
	hasMobile := has("ios") || has("android") || has("Podfile") || has("build.gradle") && has("AndroidManifest.xml")
	hasLibrary := has("go.mod") && has("pkg") && !has("cmd") && !has("main.go")
	hasDocs := has("mkdocs.yml") || has("docusaurus.config.js") || has("docs") && !hasBackend && !hasFrontend

	// Ops takes priority (infra projects are distinctive).
	if hasOps && !hasBackend && !hasFrontend {
		return "ops"
	}

	// Fullstack: both backend and frontend markers.
	if hasBackend && hasFrontend {
		return "fullstack"
	}

	if hasFrontend {
		return "frontend"
	}

	if hasData {
		return "data"
	}

	if hasMobile {
		return "mobile"
	}

	if hasLibrary {
		return "library"
	}

	if hasBackend {
		return "backend"
	}

	if hasDocs {
		return "docs"
	}

	return ""
}
