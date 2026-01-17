# Repository Consolidation Plan: bwprobe → fbforward

## Executive Summary

Migrate all code from `bwprobe/` repository into `fbforward/` repository to create a unified networking tools monorepo. The migration will preserve both tools as separate binaries while sharing the fbforward module path and build infrastructure.

**Key architectural decision**: Use a hierarchical subsystem layout where all bwprobe code lives under `bwprobe/` with its own `pkg/` and `internal/` directories, completely isolating it from fbforward internals.

## Analysis

### Current State

**bwprobe structure:**
- Module: `bwprobe`
- Binary: `cmd/bwprobe/main.go`
- Public API: `probe/` (7 files - config, errors, doc, probe, results, rtt, sampler)
- Internal packages: `engine/`, `metrics/`, `network/`, `progress/`, `protocol/`, `rpc/`, `server/`, `transport/`, `util/`
- Dependencies: `golang.org/x/sys@v0.37.0`, `github.com/google/uuid@v1.6.0`
- Docs: `README.md`, `CLAUDE.md`, `algorithm.md`, `REFACTORING.md`, `OPTIMIZATION_SUMMARY.md`, `doc/` (3 files), `examples/` (empty)
- Test: `test/integration/`, `test/testdata/`

**fbforward structure:**
- Module: `github.com/NodePath81/fbforward`
- Binary: `cmd/fbforward/main.go`
- Internal packages: `app/`, `config/`, `control/`, `forwarding/`, `metrics/`, `probe/`, `resolver/`, `shaping/`, `upstream/`, `util/`, `version/`
- Dependencies: gorilla/websocket, vishvananda/netlink, golang.org/x/net, golang.org/x/sys@v0.28.0, gopkg.in/yaml.v3
- Docs: `README.md`, `AGENT.md`, `docs/` (2 files)
- Frontend: `web/` (SPA with TypeScript/Vite)
- Deployment: `deploy/` (Debian packaging, systemd)

### Package Conflict Resolution

Using hierarchical subsystem layout **eliminates all naming conflicts**:

- bwprobe packages live under `bwprobe/pkg/` and `bwprobe/internal/`
- fbforward packages live under `internal/` (fbforward-only)
- No renaming required - original package names preserved
- Clear subsystem boundaries

## Target Layout

```
fbforward/
├── cmd/
│   ├── fbforward/                    # Existing fbforward binary
│   │   └── main.go
│   └── bwprobe/                      # Migrated bwprobe binary
│       └── main.go
│
├── bwprobe/                          # NEW: bwprobe subsystem (isolated)
│   ├── pkg/                          # NEW: Public API (from bwprobe/probe/)
│   │   ├── doc.go
│   │   ├── config.go
│   │   ├── errors.go
│   │   ├── probe.go
│   │   ├── results.go
│   │   ├── rtt.go
│   │   └── sampler.go
│   └── internal/                     # NEW: bwprobe internals (from bwprobe/internal/)
│       ├── engine/                   # from bwprobe/internal/engine/
│       ├── metrics/                  # from bwprobe/internal/metrics/
│       ├── network/                  # from bwprobe/internal/network/
│       ├── progress/                 # from bwprobe/internal/progress/
│       ├── protocol/                 # from bwprobe/internal/protocol/
│       ├── rpc/                      # from bwprobe/internal/rpc/
│       ├── server/                   # from bwprobe/internal/server/
│       ├── transport/                # from bwprobe/internal/transport/
│       └── util/                     # from bwprobe/internal/util/
│
├── internal/                         # Existing: fbforward-only internals
│   ├── app/                          # fbforward
│   ├── config/                       # fbforward
│   ├── control/                      # fbforward
│   ├── forwarding/                   # fbforward
│   ├── metrics/                      # fbforward (Prometheus)
│   ├── probe/                        # fbforward (ICMP)
│   ├── resolver/                     # fbforward
│   ├── shaping/                      # fbforward
│   ├── upstream/                     # fbforward
│   ├── util/                         # fbforward
│   └── version/                      # fbforward
│
├── docs/
│   ├── codebase.md                   # Existing fbforward
│   ├── configuration.md              # Existing fbforward
│   ├── README.md                     # NEW: Documentation index
│   ├── bwprobe/                      # NEW: bwprobe documentation
│   │   ├── codebase.md               # from bwprobe/doc/codebase.md
│   │   ├── readme.md                 # from bwprobe/doc/readme.md
│   │   ├── rpc-protocol.md           # from bwprobe/doc/rpc-protocol.md
│   │   └── algorithm.md              # from bwprobe/algorithm.md
│   └── archive/                      # NEW: Historical reference
│       ├── bwprobe-refactoring.md    # from bwprobe/REFACTORING.md
│       └── bwprobe-optimization.md   # from bwprobe/OPTIMIZATION_SUMMARY.md
│
├── test/
│   └── bwprobe/                      # NEW: from bwprobe/test/
│       ├── integration/
│       └── testdata/
│
├── web/                              # Existing fbforward UI
├── deploy/                           # Existing fbforward deployment
├── build/                            # Existing fbforward build output
├── configs/                          # Existing fbforward configs
├── task-log/                         # Both (keep separate subdirectories)
│
├── go.mod                            # MERGE dependencies
├── go.sum                            # REGENERATE
├── Makefile                          # UPDATE to build both binaries
├── README.md                         # UPDATE to describe monorepo
├── CLAUDE.md                         # ALREADY EXISTS at root (monorepo version)
└── .gitignore                        # MERGE from both
```

### Import Path Mapping

**Old bwprobe imports → New imports:**

```
"bwprobe/probe"               → "github.com/NodePath81/fbforward/bwprobe/pkg"
"bwprobe/internal/engine"     → "github.com/NodePath81/fbforward/bwprobe/internal/engine"
"bwprobe/internal/metrics"    → "github.com/NodePath81/fbforward/bwprobe/internal/metrics"
"bwprobe/internal/network"    → "github.com/NodePath81/fbforward/bwprobe/internal/network"
"bwprobe/internal/progress"   → "github.com/NodePath81/fbforward/bwprobe/internal/progress"
"bwprobe/internal/protocol"   → "github.com/NodePath81/fbforward/bwprobe/internal/protocol"
"bwprobe/internal/rpc"        → "github.com/NodePath81/fbforward/bwprobe/internal/rpc"
"bwprobe/internal/server"     → "github.com/NodePath81/fbforward/bwprobe/internal/server"
"bwprobe/internal/transport"  → "github.com/NodePath81/fbforward/bwprobe/internal/transport"
"bwprobe/internal/util"       → "github.com/NodePath81/fbforward/bwprobe/internal/util"
```

**Key advantages:**
- No package renaming needed (keep original names: `engine`, `metrics`, `util`)
- Clear subsystem isolation (all bwprobe code under `bwprobe/`)
- No conflicts with fbforward's `internal/` packages
- Easy to understand and maintain

## Migration Steps

### Phase 1: Preparation (No Code Movement)

1. **Create migration branch**
   ```bash
   cd fbforward
   git checkout -b merge-bwprobe
   ```

2. **Backup current state**
   ```bash
   git tag pre-bwprobe-merge
   ```

3. **Verify clean working tree**
   ```bash
   git status
   # Ensure both repos have no uncommitted changes
   ```

### Phase 2: Documentation Migration

4. **Create bwprobe docs directory**
   ```bash
   mkdir -p docs/bwprobe docs/archive
   ```

5. **Copy bwprobe documentation**
   ```bash
   # Main technical docs
   cp ../bwprobe/doc/codebase.md docs/bwprobe/
   cp ../bwprobe/doc/readme.md docs/bwprobe/
   cp ../bwprobe/doc/rpc-protocol.md docs/bwprobe/
   cp ../bwprobe/algorithm.md docs/bwprobe/

   # Historical/reference docs
   cp ../bwprobe/REFACTORING.md docs/archive/bwprobe-refactoring.md
   cp ../bwprobe/OPTIMIZATION_SUMMARY.md docs/archive/bwprobe-optimization.md
   ```

6. **Update root README.md**
   - Add section describing the monorepo structure
   - Reference both fbforward and bwprobe tools
   - Link to respective documentation in docs/

### Phase 3: Code Migration - bwprobe Subsystem

7. **Create bwprobe subsystem directories**
   ```bash
   mkdir -p bwprobe/pkg bwprobe/internal
   ```

8. **Copy public API to bwprobe/pkg/**
   ```bash
   cp ../bwprobe/probe/*.go bwprobe/pkg/
   ```

9. **Copy internal packages to bwprobe/internal/**
   ```bash
   # Copy all internal packages (no renaming needed)
   cp -r ../bwprobe/internal/engine bwprobe/internal/
   cp -r ../bwprobe/internal/metrics bwprobe/internal/
   cp -r ../bwprobe/internal/network bwprobe/internal/
   cp -r ../bwprobe/internal/progress bwprobe/internal/
   cp -r ../bwprobe/internal/protocol bwprobe/internal/
   cp -r ../bwprobe/internal/rpc bwprobe/internal/
   cp -r ../bwprobe/internal/server bwprobe/internal/
   cp -r ../bwprobe/internal/transport bwprobe/internal/
   cp -r ../bwprobe/internal/util bwprobe/internal/
   ```

### Phase 4: Update Package Declarations and Imports

10. **Update package declaration in bwprobe/pkg/*.go**

    Change `package probe` to `package pkg` in all 7 files:
    ```bash
    find bwprobe/pkg -name "*.go" -type f -exec sed -i 's/^package probe$/package pkg/g' {} +
    ```

11. **Update import paths in all bwprobe files**

    Replace all imports from old paths to new hierarchical paths:
    ```bash
    # Update imports in bwprobe/pkg/
    find bwprobe/pkg -name "*.go" -type f -exec sed -i 's|"bwprobe/probe"|"github.com/NodePath81/fbforward/bwprobe/pkg"|g' {} +
    find bwprobe/pkg -name "*.go" -type f -exec sed -i 's|"bwprobe/internal/|"github.com/NodePath81/fbforward/bwprobe/internal/|g' {} +

    # Update imports in bwprobe/internal/
    find bwprobe/internal -name "*.go" -type f -exec sed -i 's|"bwprobe/probe"|"github.com/NodePath81/fbforward/bwprobe/pkg"|g' {} +
    find bwprobe/internal -name "*.go" -type f -exec sed -i 's|"bwprobe/internal/|"github.com/NodePath81/fbforward/bwprobe/internal/|g' {} +
    ```

### Phase 5: Binary Migration

12. **Copy bwprobe command**
    ```bash
    mkdir -p cmd/bwprobe
    cp ../bwprobe/cmd/bwprobe/main.go cmd/bwprobe/
    ```

13. **Update cmd/bwprobe/main.go imports**
    ```bash
    sed -i 's|"bwprobe/probe"|"github.com/NodePath81/fbforward/bwprobe/pkg"|g' cmd/bwprobe/main.go
    sed -i 's|"bwprobe/internal/|"github.com/NodePath81/fbforward/bwprobe/internal/|g' cmd/bwprobe/main.go
    ```

### Phase 6: Test Migration

14. **Copy test infrastructure**
    ```bash
    mkdir -p test/bwprobe
    cp -r ../bwprobe/test/* test/bwprobe/
    ```

15. **Update test imports** (if tests reference packages)
    ```bash
    find test/bwprobe -name "*.go" -type f -exec sed -i 's|"bwprobe/probe"|"github.com/NodePath81/fbforward/bwprobe/pkg"|g' {} +
    find test/bwprobe -name "*.go" -type f -exec sed -i 's|"bwprobe/internal/|"github.com/NodePath81/fbforward/bwprobe/internal/|g' {} +
    ```

### Phase 7: Build Infrastructure

16. **Update go.mod**

    Manually edit `go.mod`:
    ```go
    module github.com/NodePath81/fbforward

    go 1.25.5  // Use newer version from bwprobe

    require (
        // Existing fbforward deps
        github.com/gorilla/websocket v1.5.3
        github.com/vishvananda/netlink v1.3.1
        golang.org/x/net v0.33.0
        gopkg.in/yaml.v3 v3.0.1

        // Added from bwprobe
        github.com/google/uuid v1.6.0

        // Merged golang.org/x/sys (use newer version)
        golang.org/x/sys v0.37.0
    )

    require github.com/vishvananda/netns v0.0.5 // indirect
    ```

17. **Regenerate go.sum**
    ```bash
    go mod tidy
    ```

18. **Update Makefile**

    Replace entire Makefile with unified version:
    ```makefile
    UI_DIR := web
    UI_VITE := $(UI_DIR)/node_modules/.bin/vite
    FBFORWARD_BIN ?= build/bin/fbforward
    BWPROBE_BIN ?= build/bin/bwprobe
    VERSION ?= dev
    LDFLAGS ?= -X github.com/NodePath81/fbforward/internal/version.Version=$(VERSION)

    .PHONY: all ui-build build build-fbforward build-bwprobe clean test

    all: build

    ui-build:
    	@if [ -x "$(UI_VITE)" ]; then \
    		npm --prefix $(UI_DIR) run build; \
    	else \
    		echo "vite not installed; skipping ui build and using existing web/dist"; \
    	fi

    build: build-fbforward build-bwprobe

    build-fbforward: ui-build
    	mkdir -p $(dir $(FBFORWARD_BIN))
    	go build -ldflags "$(LDFLAGS)" -o $(FBFORWARD_BIN) ./cmd/fbforward

    build-bwprobe:
    	mkdir -p $(dir $(BWPROBE_BIN))
    	go build -o $(BWPROBE_BIN) ./cmd/bwprobe

    test:
    	go test ./...

    clean:
    	rm -rf web/dist build/bin
    ```

19. **Merge .gitignore**

    Combine entries from both, typical additions from bwprobe:
    ```
    # Binaries
    bwprobe
    fbforward
    build/bin/

    # Go
    *.test
    *.prof

    # IDE
    .vscode/
    .idea/

    # Existing fbforward entries...
    ```

### Phase 8: Verification

20. **Verify Go build**
    ```bash
    go mod tidy
    go build ./...
    ```

21. **Build both binaries**
    ```bash
    make build
    # Should create build/bin/fbforward and build/bin/bwprobe
    ```

22. **Run tests**
    ```bash
    go test ./...
    ```

23. **Verify binary functionality**
    ```bash
    ./build/bin/fbforward --help
    ./build/bin/bwprobe --help
    ```

### Phase 9: Documentation Updates

24. **Update root README.md**

    Add monorepo introduction:
    ```markdown
    # Network Tools Monorepo

    This repository contains two Linux-only network quality and forwarding tools:

    ## fbforward

    TCP/UDP port forwarder with ICMP-based upstream quality selection.

    [Full documentation →](docs/README.md)

    ## bwprobe

    Network quality measurement tool that tests at a user-specified bandwidth cap.

    [Full documentation →](docs/bwprobe/)

    ## Building

    ```bash
    make build          # Build both binaries
    make build-fbforward
    make build-bwprobe
    ```

    ## Requirements

    - Linux only
    - Go 1.25.5+
    - For fbforward: Node.js + npm (for web UI)
    ```

25. **Update CLAUDE.md**
    - Already exists at root with monorepo documentation
    - Update import paths to reflect hierarchical structure:
      - Public API: `github.com/NodePath81/fbforward/bwprobe/pkg`
      - Internals: `github.com/NodePath81/fbforward/bwprobe/internal/*`

26. **Create docs/README.md** (navigation hub)
    ```markdown
    # Documentation

    ## fbforward

    - [Codebase Overview](codebase.md)
    - [Configuration Reference](configuration.md)

    ## bwprobe

    - [Overview](bwprobe/readme.md)
    - [Codebase Architecture](bwprobe/codebase.md)
    - [RPC Protocol](bwprobe/rpc-protocol.md)
    - [Algorithm Details](bwprobe/algorithm.md)

    ## Historical

    - [bwprobe Refactoring Notes](archive/bwprobe-refactoring.md)
    - [bwprobe Optimization Summary](archive/bwprobe-optimization.md)
    ```

### Phase 10: Cleanup

27. **Remove backup files**
    ```bash
    # bwprobe had main.go.backup - don't copy this
    ```

28. **Update task-log** (optional)
    - Decide whether to merge task-log directories or keep separate
    - Recommendation: Keep separate subdirectories
    ```bash
    mkdir -p task-log/bwprobe
    cp -r ../bwprobe/task-log/* task-log/bwprobe/
    ```

29. **Commit migration**
    ```bash
    git add .
    git commit -m "Merge bwprobe repository into fbforward monorepo

    - Created hierarchical bwprobe subsystem under bwprobe/
    - Migrated public API to bwprobe/pkg/ package
    - Migrated internals to bwprobe/internal/ (no renaming)
    - Updated all import paths to use hierarchical structure
    - Merged go.mod dependencies
    - Updated Makefile to build both binaries
    - Consolidated documentation under docs/
    - Updated README.md and CLAUDE.md for monorepo structure
    "
    ```

## Special Cases and Considerations

### 1. Dependency Version Conflicts

- **golang.org/x/sys**: bwprobe uses v0.37.0, fbforward uses v0.28.0
  - **Resolution**: Use bwprobe's newer v0.37.0
  - **Risk**: Low, backward compatible
  - **Verification**: Run full test suite after merge

### 2. Go Version Alignment

- bwprobe requires Go 1.25.5, fbforward requires Go 1.25.4
  - **Resolution**: Bump to Go 1.25.5
  - **Impact**: None (minor version increase)

### 3. Package Naming - No Conflicts!

**Key advantage of hierarchical structure**: No package renaming needed
  - All bwprobe packages keep original names (engine, metrics, util, etc.)
  - Isolated under `bwprobe/internal/`
  - fbforward packages remain under `internal/`
  - **Files affected**: ~50+ Go files
  - **Automation**: Simple sed commands in steps 10-15
  - **Verification**: `go build ./...` must succeed without errors

### 4. Public API Visibility

bwprobe's `probe/` package becomes `bwprobe/pkg/`:
  - **Old import**: `import "bwprobe/probe"`
  - **New import**: `import "github.com/NodePath81/fbforward/bwprobe/pkg"`
  - **Impact**: Breaking change for external users (if any)
  - **Mitigation**: Document in CHANGELOG, consider keeping old repo with deprecation notice

### 5. Binary Outputs

Both binaries will coexist:
  - `build/bin/fbforward` (existing)
  - `build/bin/bwprobe` (new)
  - No naming conflicts
  - Update deployment scripts if needed

### 6. Testing

- bwprobe has integration tests in `test/integration/`
- fbforward currently has no test directory
- **Resolution**: Keep bwprobe tests under `test/bwprobe/`
- Run separately or integrate into CI

### 7. Documentation Consolidation

Two doc directories exist:
  - bwprobe has `doc/` (3 files) and `docs/` (empty)
  - fbforward has `docs/` (2 files)
  - **Resolution**: Everything under `docs/` with subdirectory `docs/bwprobe/`

### 8. Web UI

fbforward has embedded web UI that bwprobe doesn't need:
  - No changes required
  - bwprobe binary won't include web assets
  - Build targets are independent

### 9. Deployment

fbforward has Debian packaging in `deploy/`:
  - Update package to include both binaries, or
  - Create separate packages (fbforward.deb and bwprobe.deb)
  - Systemd units may need updates

### 10. Git History

bwprobe's git history will be lost in this approach:
  - **Alternative**: Use `git subtree` or `git submodule` to preserve history
  - **Recommendation**: For clean integration, accept history loss but keep old repo
  - Tag the old bwprobe repo as `archived-merged-to-fbforward`

### 11. Subsystem Isolation Benefits

The hierarchical structure provides:
  - **Clear ownership**: All bwprobe code under `bwprobe/`
  - **No naming collisions**: bwprobe and fbforward packages completely isolated
  - **Easier maintenance**: Can evolve subsystems independently
  - **Better discoverability**: Structure mirrors project boundaries
  - **Future extensibility**: Easy to add more subsystems (e.g., `tool3/pkg/`, `tool3/internal/`)

## Rollback Plan

If migration fails or causes issues:

1. **Immediate rollback**:
   ```bash
   git checkout main
   git branch -D merge-bwprobe
   ```

2. **Restore from tag**:
   ```bash
   git reset --hard pre-bwprobe-merge
   ```

3. **Keep repos separate**: Continue developing independently if integration proves problematic

## Post-Migration Tasks

1. **Update CI/CD pipelines** to build both binaries
2. **Update deployment scripts** for both tools
3. **Archive old bwprobe repository** with README pointing to new location
4. **Notify users** of import path changes (if public API is used externally)
5. **Update any external documentation** or wikis
6. **Create GitHub release** noting the merge
7. **Update package managers** if distributed via APT/YUM/etc.
8. **Update CLAUDE.md** at root to reflect hierarchical structure

## Validation Checklist

- [ ] All Go files compile without errors (`go build ./...`)
- [ ] All tests pass (`go test ./...`)
- [ ] Both binaries build successfully (`make build`)
- [ ] fbforward binary runs and serves UI
- [ ] bwprobe binary runs client/server modes
- [ ] No import cycle errors
- [ ] Documentation links are valid
- [ ] Makefile targets work correctly
- [ ] go.mod has correct dependencies
- [ ] .gitignore covers both projects
- [ ] Package names preserved (no `bw` prefixes needed)
- [ ] Subsystem boundaries clear (bwprobe/ vs internal/)

## Timeline Estimate

- Phase 1-2 (Prep + Docs): 30 minutes
- Phase 3 (Copy subsystem): 15 minutes
- Phase 4 (Update imports): 30 minutes
- Phase 5-6 (Binary + Tests): 30 minutes
- Phase 7 (Build infra): 30 minutes
- Phase 8 (Verification): 1 hour
- Phase 9-10 (Docs + Cleanup): 30 minutes

**Total**: 3-4 hours for careful execution (faster than flat structure due to simpler renaming)

## Success Criteria

1. Both `fbforward` and `bwprobe` binaries build and run correctly
2. All existing tests pass
3. Import paths use unified module name with hierarchical structure
4. Documentation is accessible and organized
5. Single Makefile builds both projects
6. No duplicate code or conflicting packages
7. Git history is clean with meaningful commit message
8. **Subsystem isolation**: bwprobe code cleanly separated under `bwprobe/`
9. **No package renaming**: Original package names preserved (engine, metrics, util)
10. **Clear boundaries**: `bwprobe/pkg/` for public API, `bwprobe/internal/` for internals
