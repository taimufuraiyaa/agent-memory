package harnesstools

import (
	"path"
	"strings"
)

// ControlPlane names the kind of project file a change touches when that file steers how
// the project is built, run, deployed or instructed rather than what it does: dependency
// manifests and lockfiles, build and CI definitions, container files, scripts, database
// migrations and schemas, and agent instruction files. A change to one of these can run
// code the next time anyone builds, so it asks with extra friction. The empty string is an
// ordinary file. Classification is by path only, deterministic, and case-insensitive.
func ControlPlane(rel string) string {
	clean := strings.ToLower(path.Clean(strings.ReplaceAll(rel, "\\", "/")))
	base := path.Base(clean)
	if kind, ok := controlPlaneNames[base]; ok {
		return kind
	}
	for _, prefix := range controlPlanePrefixes {
		if strings.HasPrefix(base, prefix.name) {
			return prefix.kind
		}
	}
	ext := path.Ext(base)
	if kind, ok := controlPlaneExtensions[ext]; ok {
		return kind
	}
	for _, segment := range strings.Split(path.Dir(clean), "/") {
		if kind, ok := controlPlaneDirs[segment]; ok {
			return kind
		}
	}
	return ""
}

var controlPlaneNames = map[string]string{
	"go.mod": "dependency_manifest", "go.sum": "dependency_manifest", "go.work": "dependency_manifest", "go.work.sum": "dependency_manifest",
	"package.json": "dependency_manifest", "package-lock.json": "dependency_manifest", "npm-shrinkwrap.json": "dependency_manifest",
	"yarn.lock": "dependency_manifest", "pnpm-lock.yaml": "dependency_manifest", "pnpm-workspace.yaml": "dependency_manifest", "bun.lockb": "dependency_manifest",
	"cargo.toml": "dependency_manifest", "cargo.lock": "dependency_manifest", "pyproject.toml": "dependency_manifest", "poetry.lock": "dependency_manifest",
	"pipfile": "dependency_manifest", "pipfile.lock": "dependency_manifest", "setup.py": "dependency_manifest", "setup.cfg": "dependency_manifest",
	"gemfile": "dependency_manifest", "gemfile.lock": "dependency_manifest", "composer.json": "dependency_manifest", "composer.lock": "dependency_manifest",
	"build.gradle": "build_script", "build.gradle.kts": "build_script", "settings.gradle": "build_script", "settings.gradle.kts": "build_script",
	"pom.xml": "build_script", "makefile": "build_script", "gnumakefile": "build_script", "justfile": "build_script", "taskfile.yml": "build_script",
	"taskfile.yaml": "build_script", "rakefile": "build_script", "cmakelists.txt": "build_script", "jenkinsfile": "ci", "procfile": "build_script",
	"vercel.json": "deploy", "netlify.toml": "deploy", "fly.toml": "deploy", "renovate.json": "dependency_manifest",
	"claude.md": "instruction_file", "agents.md": "instruction_file", "gemini.md": "instruction_file",
	"schema.sql": "migration", "structure.sql": "migration", "schema.prisma": "migration",
}

var controlPlanePrefixes = []struct{ name, kind string }{
	{"dockerfile", "container"}, {"docker-compose", "container"}, {"containerfile", "container"}, {"requirements", "dependency_manifest"},
}

var controlPlaneExtensions = map[string]string{
	".sh": "shell_script", ".bash": "shell_script", ".zsh": "shell_script", ".fish": "shell_script", ".ps1": "shell_script", ".bat": "shell_script", ".cmd": "shell_script",
	".mk": "build_script", ".tf": "infra", ".tfvars": "infra",
}

var controlPlaneDirs = map[string]string{
	"migrations": "migration", "migrate": "migration", "scripts": "shell_script", "script": "shell_script", "bin": "shell_script", "hack": "shell_script",
	"ci": "ci", "deploy": "deploy", "terraform": "infra", "k8s": "infra", "helm": "infra", "charts": "infra",
}
