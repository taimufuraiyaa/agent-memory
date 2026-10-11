package harnesstools

import "testing"

func TestControlPlaneClassifiesByPathOnly(t *testing.T) {
	for path, want := range map[string]string{
		"go.mod": "dependency_manifest", "go.sum": "dependency_manifest", "sub/pkg/go.mod": "dependency_manifest", "GO.MOD": "dependency_manifest",
		"package.json": "dependency_manifest", "web/package-lock.json": "dependency_manifest", "pnpm-lock.yaml": "dependency_manifest",
		"requirements.txt": "dependency_manifest", "requirements-dev.txt": "dependency_manifest", "Cargo.toml": "dependency_manifest",
		"Makefile": "build_script", "makefile": "build_script", "rules.mk": "build_script", "CMakeLists.txt": "build_script", "build.gradle.kts": "build_script",
		"Dockerfile": "container", "Dockerfile.prod": "container", "docker-compose.yml": "container", "services/api/Dockerfile": "container",
		"install.sh": "shell_script", "tools/run.PS1": "shell_script", "build.bat": "shell_script", "scripts/anything.txt": "shell_script", "bin/tool": "shell_script",
		"db/migrations/001_init.sql": "migration", "migrate/up.go": "migration", "schema.sql": "migration", "prisma/schema.prisma": "migration",
		"CLAUDE.md": "instruction_file", "docs/AGENTS.md": "instruction_file", "Jenkinsfile": "ci", "ci/pipeline.yml": "ci",
		"main.tf": "infra", "k8s/deploy.yaml": "infra", "deploy/app.yaml": "deploy", "vercel.json": "deploy",
		"a\\b\\go.mod": "dependency_manifest", "./go.mod": "dependency_manifest", "x/../go.mod": "dependency_manifest",
		// ordinary files
		"main.go": "", "internal/app/app.go": "", "README.md": "", "docs/guide.md": "", "src/index.ts": "", "mod.go": "", "scripts.go": "", "binary.go": "",
		"notes/migrations-notes.txt": "", "src/schema.go": "", "tests/ci_test.go": "",
	} {
		if got := ControlPlane(path); got != want {
			t.Errorf("ControlPlane(%q) = %q, want %q", path, got, want)
		}
	}
}
