// SPDX-License-Identifier: BSD-2-Clause

package container

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/assets"
	"github.com/alchemillahq/sylve/internal/db/models"
	containerModels "github.com/alchemillahq/sylve/internal/db/models/container"
	containerServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/container"
	"github.com/alchemillahq/sylve/internal/testutil"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db := testutil.NewSQLiteTestDB(t, &models.BasicSettings{})
	if err := containerModels.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.BasicSettings{Pools: []string{"tank"}, Services: []models.AvailableService{models.Containers}}).Error; err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, DataDir: t.TempDir()}
}

func TestCreateSpecIsWhatPodmanCreateFlagsWouldSend(t *testing.T) {
	network := &containerModels.Network{ID: 1, Name: "default", Bridge: "sylc1"}
	record := containerModels.Container{OCID: 7, Name: "Web", Image: "docker.io/library/nginx", OS: "linux",
		Command: []string{"nginx", "-g", "daemon off;"}, Entrypoint: []string{"/entry"}, Network: network, IP: "10.89.0.7",
		Env:     []containerModels.EnvVar{{Name: "TOKEN", Value: "secret"}},
		Ports:   []containerModels.Port{{Protocol: "tcp", HostPort: 8080, ContainerPort: 80}, {Protocol: "udp", HostIP: "127.0.0.1", HostPort: 53, ContainerPort: 53}},
		Mounts:  []containerModels.Mount{{Type: containerModels.MountVolume, Source: "data", Target: "/data", ReadOnly: true}},
		Restart: "on-failure", RestartRetries: 3, StopTimeout: 10,
		Annotations: map[string]string{"org.freebsd.jail.allow.mlock": "true"}, ExtraHosts: []string{"db:10.0.0.2"}, ShmSize: "1gb"}
	same := func(spec createSpec, want string) {
		t.Helper()
		data, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		var got, expected any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("create spec\n%s\nwant\n%s", data, want)
		}
	}

	same(newCreateSpec(record, []string{"/tank/sylve/containers/7/data"}), fmt.Sprintf(`{
		"name": "sylve-7", "labels": {"io.sylve.ocid": "7"},
		"image": "docker.io/library/nginx", "image_os": "linux", "image_arch": %q, "stop_timeout": 10,
		"netns": {"nsmode": "bridge"}, "Networks": {"sylve-default": {"static_ips": ["10.89.0.7"], "aliases": ["web"]}},
		"hostname": "Web", "env": {"TOKEN": "secret"}, "annotations": {"org.freebsd.jail.allow.mlock": "true"},
		"hostadd": ["db:10.0.0.2"], "shm_size": 1073741824,
		"portmappings": [{"host_port": 8080, "container_port": 80, "protocol": "tcp"},
			{"host_ip": "127.0.0.1", "host_port": 53, "container_port": 53, "protocol": "udp"}],
		"mounts": [{"destination": "/data", "type": "nullfs", "source": "/tank/sylve/containers/7/data", "options": ["ro"]}],
		"umask": "0022", "restart_policy": "on-failure", "restart_tries": 3,
		"entrypoint": ["/entry"], "command": ["nginx", "-g", "daemon off;"]}`, runtime.GOARCH))

	stack := &containerModels.Network{ID: 2, Name: "weavestream", Bridge: "sylc2"}
	member := containerModels.Container{OCID: 9, Name: "API", Image: "api", OS: "freebsd", Network: stack,
		Stack: "weavestream", Service: "api", Restart: "no", StopTimeout: 10}
	if aliases := newCreateSpec(member, nil).Networks["sylve-weavestream"].Aliases; !slices.Equal(aliases, []string{"api"}) {
		t.Fatal("a stack member answers to its service name, once", aliases)
	}
	member.Name = "Backend"
	if aliases := newCreateSpec(member, nil).Networks["sylve-weavestream"].Aliases; !slices.Equal(aliases, []string{"backend", "api"}) {
		t.Fatal("renamed, a stack member still answers to its service name", aliases)
	}
	member.Network = network
	if aliases := newCreateSpec(member, nil).Networks["sylve-default"].Aliases; !slices.Equal(aliases, []string{"backend"}) {
		t.Fatal("service names stay on the stack's own network", aliases)
	}

	record = containerModels.Container{OCID: 8, Name: "Host", Image: "redis", OS: "freebsd", Entrypoint: []string{},
		Restart: "no", StopTimeout: 5}
	same(newCreateSpec(record, nil), fmt.Sprintf(`{
		"name": "sylve-8", "labels": {"io.sylve.ocid": "8"}, "image": "redis", "image_os": "freebsd", "image_arch": %q,
		"stop_timeout": 5, "netns": {"nsmode": "host"}, "umask": "0022", "restart_policy": "no"}`, runtime.GOARCH))

	for size, want := range map[string]int64{"512": 512, "64b": 64, "2K": 2 << 10, "64m": 64 << 20, "1gb": 1 << 30} {
		if got := shmBytes(size); got != want {
			t.Fatalf("shmBytes(%q) = %d, want %d", size, got, want)
		}
	}
}

func TestConflistJoinsSwitchesWithoutTakingTheGateway(t *testing.T) {
	service := newTestService(t)
	switchID := uint(3)
	for _, network := range []containerModels.Network{
		{Name: "default", Bridge: "sylc1", Subnet: "10.89.0.0/24", Gateway: "10.89.0.1"},
		{Name: "lan", SwitchID: &switchID, Bridge: "lan0", Subnet: "192.168.1.0/24", Gateway: "192.168.1.1", RangeStart: "192.168.1.200", RangeEnd: "192.168.1.250"},
	} {
		if err := service.writeConflist(network); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(service.DataDir, "cni", "net.d", "sylve-"+network.Name+".conflist"))
		if err != nil {
			t.Fatal(err)
		}
		var conflist struct {
			Name    string           `json:"name"`
			Plugins []map[string]any `json:"plugins"`
		}
		if err := json.Unmarshal(data, &conflist); err != nil {
			t.Fatal(err)
		}
		bridge := conflist.Plugins[0]
		nat := network.SwitchID == nil
		if conflist.Name != network.PodmanName() || bridge["bridge"] != network.Bridge || bridge["isGateway"] != nat || bridge["ipMasq"] != nat {
			t.Fatal("unexpected bridge plugin", network.Name, bridge)
		}
		if nat != (len(conflist.Plugins) == 5) {
			t.Fatal("only NAT networks get port mapping, firewall and DNS plugins", network.Name, len(conflist.Plugins))
		}
		if !strings.Contains(string(data), `"gw": "`+network.Gateway+`"`) {
			t.Fatal("default route must use the network gateway", string(data))
		}
	}
}

func TestNormalizeSpecDefaultsAndRejections(t *testing.T) {
	service := newTestService(t)
	ctx := t.Context()
	spec, network, err := service.normalizeSpec(ctx, containerServiceInterfaces.Spec{Image: "alpine", Network: "host"})
	if err != nil || network != nil || spec.OS != "" || spec.Restart != "no" || spec.StopTimeout != 10 {
		t.Fatal("unexpected defaults", spec, network, err)
	}
	for name, bad := range map[string]containerServiceInterfaces.Spec{
		"host ports":    {Image: "alpine", Network: "host", Ports: []containerModels.Port{{HostPort: 80, ContainerPort: 80}}},
		"duplicate env": {Image: "alpine", Network: "host", Env: []containerModels.EnvVar{{Name: "A"}, {Name: "A"}}},
		"root target":   {Image: "alpine", Network: "host", Mounts: []containerModels.Mount{{Type: containerModels.MountVolume, Source: "data", Target: "/"}}},
		"missing path":  {Image: "alpine", Network: "host", Mounts: []containerModels.Mount{{Type: containerModels.MountPath, Source: filepath.Join(t.TempDir(), "missing"), Target: "/data"}}},
		"bad os":        {Image: "alpine", Network: "host", OS: "windows"},
		"no network":    {Image: "alpine", Network: "missing"},
	} {
		if _, _, err := service.normalizeSpec(ctx, bad); err == nil {
			t.Fatal("expected rejection:", name)
		}
	}
}

func TestDatasetsInUseFindsMountedDatasets(t *testing.T) {
	service := newTestService(t)
	record := containerModels.Container{OCID: 1, Name: "db", Pool: "tank", Image: "postgres", RuntimeID: "x",
		Mounts: []containerModels.Mount{{Type: containerModels.MountDataset, Source: "tank/pg", Target: "/var/lib/postgresql", GUID: "42"}}}
	if err := service.DB.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	for guids, want := range map[string]bool{"42": true, "43": false} {
		inUse, err := containerModels.DatasetsInUse(service.DB, []string{guids})
		if err != nil || inUse != want {
			t.Fatal("unexpected usage", guids, inUse, err)
		}
	}
}

func newCatalogService(t *testing.T) *Service {
	service := newTestService(t)
	installBuiltinCatalog(t, service.DataDir)
	return service
}

func installBuiltinCatalog(t *testing.T, dataDir string) {
	t.Helper()
	catalog, err := fs.Sub(assets.CatalogFiles, "catalog")
	if err == nil {
		err = os.CopyFS(filepath.Join(dataDir, "catalog"), catalog)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestCatalogFallsBackToBuiltinCopy(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	sources := catalogSources
	catalogSources = map[string]string{"daemonless": server.URL}
	t.Cleanup(func() { catalogSources = sources })

	service := newTestService(t)
	if _, err := service.CatalogTemplate(t.Context(), "daemonless", "redis"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(service.path("catalog", "daemonless", "manifests", "redis.yaml"))
	if err != nil || time.Since(info.ModTime()) < catalogTTL {
		t.Fatal("the built-in copy is replaced once the catalog is reachable", info, err)
	}
}

func renderSingle(t *testing.T, service *Service, app string, values map[string]string, choices map[string]string) (containerServiceInterfaces.CreateRequest, error) {
	t.Helper()
	plan, err := renderDefaults(t, service, app, values, choices)
	if err != nil {
		return containerServiceInterfaces.CreateRequest{}, err
	}
	if plan.Stack != "" || len(plan.Containers) != 1 {
		t.Fatal("expected one container", plan.Stack, len(plan.Containers))
	}
	return plan.Containers[0], nil
}

func renderDefaults(t *testing.T, service *Service, app string, values map[string]string, choices map[string]string) (containerServiceInterfaces.CatalogPlan, error) {
	t.Helper()
	source, id, _ := strings.Cut(app, "/")
	template, err := service.CatalogTemplate(t.Context(), source, id)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{}
	for _, variable := range template.Variables {
		all[variable.Name] = variable.Default
	}
	for name, value := range values {
		all[name] = value
	}
	return service.RenderCatalogApp(t.Context(), source, id,
		containerServiceInterfaces.CatalogRenderRequest{Values: all, Choices: choices})
}

func TestRenderCatalogAppFromManifest(t *testing.T) {
	service := newCatalogService(t)
	create, err := renderSingle(t, service, "daemonless/jellyfin",
		map[string]string{"WEB_PORT": "9000", "TV_PATH": "/srv/tv", "MOVIES_PATH": "tank/media/movies"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := create.Spec
	if create.Name != "jellyfin" || spec.Image != "ghcr.io/daemonless/jellyfin:pkg-latest" || spec.OS != "freebsd" ||
		spec.Restart != "unless-stopped" || spec.Network != containerServiceInterfaces.DefaultNetwork {
		t.Fatal("unexpected container", create.Name, spec.Image, spec.OS, spec.Restart, spec.Network)
	}
	if !slices.Equal(spec.Ports, []containerModels.Port{{Protocol: "tcp", HostPort: 9000, ContainerPort: 8096}}) || create.WebPort != 8096 {
		t.Fatal("the web UI must be published on the chosen host port", spec.Ports, create.WebPort)
	}
	want := []containerModels.Mount{
		{Type: containerModels.MountVolume, Source: "config", Target: "/config", Owner: "1000:1000", Mode: "755"},
		{Type: containerModels.MountPath, Source: "/srv/tv", Target: "/tv"},
		{Type: containerModels.MountDataset, Source: "tank/media/movies", Target: "/movies"},
	}
	if !slices.Equal(spec.Mounts, want) {
		t.Fatal("paths, datasets and volumes are told apart and empty optional directories are left out", spec.Mounts)
	}
	if spec.Annotations["org.freebsd.jail.allow.mlock"] != "true" || !slices.Contains(spec.Env, containerModels.EnvVar{Name: "TZ", Value: "UTC"}) {
		t.Fatal("jail permissions and environment come from the manifest", spec.Annotations, spec.Env)
	}
}

func TestCatalogDirectoriesLeftEmptyBecomeVolumes(t *testing.T) {
	service := newCatalogService(t)
	template, err := service.CatalogTemplate(t.Context(), "daemonless", "adguardhome")
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range template.Variables {
		if variable.Name == "CONF_PATH" && (variable.Volume != "conf" || variable.Owner != "1000:1000") {
			t.Fatal("the template names the volume a directory gets and who owns it", variable)
		}
		if want := map[string]string{"WEB_PORT": "tcp", "PORT_53": "tcp", "PORT_53_UDP": "udp"}[variable.Name]; want != "" && variable.Protocol != want {
			t.Fatal("port settings say which protocol they publish", variable)
		}
	}
	create, err := renderSingle(t, service, "daemonless/adguardhome", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []containerModels.Mount{
		{Type: containerModels.MountVolume, Source: "conf", Target: "/opt/adguardhome/conf", Owner: "1000:1000"},
		{Type: containerModels.MountVolume, Source: "work", Target: "/opt/adguardhome/work", Owner: "1000:1000"},
	}
	if !slices.Equal(create.Spec.Mounts, want) {
		t.Fatal("required directories left empty become volumes the app can write", create.Spec.Mounts)
	}
}

func TestCatalogHintExamplesBecomePlaceholders(t *testing.T) {
	service := newCatalogService(t)
	for app, want := range map[string]map[string]string{
		"adguardhome-sync": {"ORIGIN_URL": "http://192.168.1.2:3000", "REPLICA1_URL": ""},
		"authelia-server":  {"PUBLIC_URL": "https://auth.example.com"},
		"memcached":        {"MEMCACHED_ARGS": "-m 256 -c 2048"},
		"vikunja":          {"VIKUNJA_SERVICE_PUBLICURL": "http://192.168.1.10:3456/"},
		"plex":             {"ALLOWED_NETWORKS": "192.168.1.0/24"},
	} {
		template, err := service.CatalogTemplate(t.Context(), "daemonless", app)
		if err != nil {
			t.Fatal(err)
		}
		for _, variable := range template.Variables {
			if placeholder, ok := want[variable.Name]; ok && variable.Placeholder != placeholder {
				t.Errorf("%s %s: placeholder %q, want %q", app, variable.Name, variable.Placeholder, placeholder)
			}
		}
	}
}

func TestCatalogParentFoldersLeftEmptyBecomeStackDatasets(t *testing.T) {
	service := newCatalogService(t)
	manifest := `services:
  api:
    image: api
    volumes:
      - ${DATA_DIR}/files:/files
      - ${DATA_DIR}/db:/db
  worker:
    image: worker
    volumes:
      - ${DATA_DIR}/files:/files
x-fjord:
  variables:
    - name: DATA_DIR
      label: Folder for uploads and the database
      type: path
      default: /containers/app
`
	if err := os.WriteFile(filepath.Join(service.DataDir, "catalog", "daemonless", "manifests", "immich.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	template, err := service.CatalogTemplate(t.Context(), "daemonless", "immich")
	if err != nil || len(template.Variables) != 1 || template.Variables[0].Target != "" ||
		!slices.Equal(template.Variables[0].Folders, []string{"db", "files"}) {
		t.Fatal("a setting holding several folders lists them", template.Variables, err)
	}

	for value, want := range map[string]func(containerModels.Mount) bool{
		"": func(mount containerModels.Mount) bool {
			return mount.Type == containerModels.MountDataset && mount.Source == "."+mount.Target && mount.Owner == "1000:1000"
		},
		"/srv/app": func(mount containerModels.Mount) bool {
			return mount.Type == containerModels.MountPath && mount.Source == "/srv/app"+mount.Target
		},
	} {
		plan, err := renderDefaults(t, service, "daemonless/immich", map[string]string{"DATA_DIR": value}, nil)
		if err != nil {
			t.Fatal("the folder setting may be left empty", err)
		}
		mounts := 0
		for _, create := range plan.Containers {
			for _, mount := range create.Mounts {
				if mounts++; !want(mount) {
					t.Fatal("left empty, each folder is a dataset under the stack; given, a folder in it", value, create.Name, mount)
				}
			}
		}
		if mounts != 3 {
			t.Fatal("every service mounts its folders", value, plan.Containers)
		}
	}
}

func envOf(create containerServiceInterfaces.CreateRequest) map[string]string {
	env := map[string]string{}
	for _, variable := range create.Env {
		env[variable.Name] = variable.Value
	}
	return env
}

func TestCatalogChoicesAddCompanionContainers(t *testing.T) {
	service := newCatalogService(t)
	template, err := service.CatalogTemplate(t.Context(), "daemonless", "gitea")
	if err != nil || len(template.Choices) != 1 || !slices.Equal(template.Containers, []string{"gitea"}) {
		t.Fatal("expected one container and the database choice", template.Containers, template.Choices, err)
	}
	adds := map[string][]string{}
	for _, option := range template.Choices[0].Options {
		adds[option.ID] = option.Adds
	}
	if len(adds["sqlite"]) != 0 || !slices.Equal(adds["postgres"], []string{"gitea-postgres"}) {
		t.Fatal("options name the containers they add", adds)
	}
	create, err := renderSingle(t, service, "daemonless/gitea", nil, nil)
	if err != nil || envOf(create)["GITEA__database__DB_TYPE"] != "sqlite3" {
		t.Fatal("the default choice sets its environment", create.Env, err)
	}

	plan, err := renderDefaults(t, service, "daemonless/gitea", nil, map[string]string{"database": "postgres"})
	if err != nil || plan.Stack != "gitea" || len(plan.Containers) != 2 {
		t.Fatal("PostgreSQL makes Gitea a stack of two containers", plan, err)
	}
	postgres, gitea := plan.Containers[0], plan.Containers[1]
	if postgres.Name != "gitea-postgres" || gitea.Name != "gitea" || postgres.Network != "gitea" || gitea.Network != "gitea" ||
		postgres.Stack != "gitea" || gitea.Stack != "gitea" {
		t.Fatal("a stack's containers share its network, the companion started first", postgres.Name, gitea.Name, postgres.Network)
	}
	app, db := envOf(gitea), envOf(postgres)
	if app["GITEA__database__DB_TYPE"] != "postgres" || app["GITEA__database__HOST"] != "postgres" ||
		app["GITEA__database__NAME"] != "gitea" || len(app["GITEA__database__PASSWD"]) != 64 ||
		db["POSTGRES_PASSWORD"] != app["GITEA__database__PASSWD"] || db["POSTGRES_DB"] != "gitea" || db["POSTGRES_USER"] != "gitea" {
		t.Fatal("the companion is found by service name and shares the generated password", app, db)
	}
	data := containerModels.Mount{Type: containerModels.MountVolume, Source: "data", Target: "/var/lib/postgresql/data", Owner: "1000:1000"}
	if !slices.Contains(postgres.Mounts, data) || postgres.Annotations["org.freebsd.jail.allow.sysvipc"] != "true" {
		t.Fatal("the companion keeps its data in a volume and gets its jail permissions", postgres.Mounts, postgres.Annotations)
	}
	if gitea.WebPort != 3000 || postgres.WebPort != 0 || len(postgres.Ports) != 0 {
		t.Fatal("only the app's own container is reached from outside", gitea.WebPort, postgres.WebPort, postgres.Ports)
	}
}

func TestEveryCatalogAppRenders(t *testing.T) {
	service := newCatalogService(t)
	if err := service.DB.Create(&containerModels.Network{Name: "default", Bridge: "sylc1", Subnet: "10.89.0.0/24", Gateway: "10.89.0.1"}).Error; err != nil {
		t.Fatal(err)
	}
	apps, err := service.Catalog(t.Context())
	if err != nil || len(apps) == 0 {
		t.Fatal("expected the catalog's apps", len(apps), err)
	}
	placeholders := map[string]string{"port": "10000", "public_url": "https://example.com", "image_tag": "latest"}
	specs := map[string]containerServiceInterfaces.Spec{}
	stacks := 1
	for _, app := range apps {
		name := app.Source + "/" + app.ID
		template, err := service.CatalogTemplate(t.Context(), app.Source, app.ID)
		if err != nil {
			t.Fatal(name, err)
		}
		required := map[string]string{}
		labels := map[string]bool{}
		for _, variable := range template.Variables {
			if !variable.Optional && variable.Default == "" {
				required[variable.Name] = cmp.Or(placeholders[variable.Type], "required")
			}
			if labels[variable.Label] {
				t.Fatal(name, "shows two settings as", variable.Label)
			}
			labels[variable.Label] = true
		}
		plan, err := renderDefaults(t, service, name, required, nil)
		if err != nil {
			t.Fatal(name, err)
		}
		if (plan.Stack != "") != app.Stack {
			t.Fatal(name, "renders as a stack only if it is one", plan.Stack)
		}
		if plan.Stack != "" {
			stacks++
			network := containerModels.Network{Name: plan.Stack, Bridge: fmt.Sprintf("sylc%d", stacks),
				Subnet: fmt.Sprintf("10.89.%d.0/24", stacks), Gateway: fmt.Sprintf("10.89.%d.1", stacks)}
			if err := service.DB.Create(&network).Error; err != nil {
				t.Fatal(err)
			}
		}
		for _, create := range plan.Containers {
			spec, _, err := service.normalizeSpec(t.Context(), create.Spec)
			if err != nil || create.CatalogApp != name || create.Stack != plan.Stack {
				t.Fatal(name, create.Name, create.CatalogApp, err)
			}
			for _, mount := range spec.Mounts {
				if mount.Type == containerModels.MountVolume && mount.Owner == "" {
					t.Fatal("volumes an app writes to must not be left to root", name, mount)
				}
			}
			specs[name] = spec
		}
	}

	bichon, wireguard, assistant := specs["daemonless/bichon"], specs["daemonless/transmission-wireguard"], specs["daemonless/home-assistant"]
	for feature, ok := range map[string]bool{
		"long-form ports":   slices.Equal(bichon.Ports, []containerModels.Port{{Protocol: "tcp", HostPort: 15630, ContainerPort: 15630}}),
		"generated secrets": len(bichon.Env) == 4 && bichon.Env[3].Name == "BICHON_ENCRYPT_PASSWORD" && len(bichon.Env[3].Value) == 64,
		"typeable login passwords": slices.ContainsFunc(specs["daemonless/paperless-ngx"].Env, func(env containerModels.EnvVar) bool {
			return env.Name == "PAPERLESS_ADMIN_PASSWORD" && len(env.Value) == 24
		}),
		"jail annotations":  wireguard.Annotations["org.freebsd.jail.vnet"] == "new",
		"udp ports":         slices.Contains(wireguard.Ports, containerModels.Port{Protocol: "udp", HostPort: 51413, ContainerPort: 51413}),
		"host network":      assistant.Network == containerServiceInterfaces.HostNetwork && len(assistant.Ports) == 0,
		"stop grace period": assistant.StopTimeout == 120 && specs["daemonless/clickhouse"].StopTimeout == 60,
		"extra hosts":       slices.Equal(specs["daemonless/opencloud"].ExtraHosts, []string{"hostname:127.0.0.1"}),
	} {
		if !ok {
			t.Error("catalog apps must keep their", feature)
		}
	}

	render := func(variant string) (containerServiceInterfaces.CreateRequest, error) {
		plan, err := service.RenderCatalogApp(t.Context(), "daemonless", "redis", containerServiceInterfaces.CatalogRenderRequest{Variant: variant})
		if err != nil {
			return containerServiceInterfaces.CreateRequest{}, err
		}
		return plan.Containers[0], nil
	}
	if create, err := render("7.4-pkg-latest"); err != nil || create.Spec.Image != "ghcr.io/daemonless/redis:7.4-pkg-latest" {
		t.Fatal("a variant picks its image", create.Spec.Image, err)
	}
	if _, err := render("latest"); err == nil || !strings.Contains(err.Error(), "catalog_variant_not_found") {
		t.Fatal("unknown variants must be refused, not fall back to the default image", err)
	}
}

func TestParseSockstat(t *testing.T) {
	output := `USER   COMMAND    PID   FD PROTO LOCAL ADDRESS                            FOREIGN ADDRESS
root   sylve      4242 10 tcp46 *:443                                    *:*
root   nfsd       13811 18 udp4  *:*                                      *:*
root   main        555  4 tcp4  127.0.0.1:8181                           *:*
nobody dnsmasq     3388 16 udp6  [fe80::7061:5eff:fe47:568e%AO2BbU3Q]:53  *:*
root   dnsmasq    67083  4 udp4  10.89.0.1:53                             *:*
`
	want := []containerServiceInterfaces.UsedPort{
		{Protocol: "tcp", Port: 443, By: "Sylve"},
		{Protocol: "tcp", Port: 8181, Address: "127.0.0.1", By: "Sylve"},
		{Protocol: "udp", Port: 53, Address: "fe80::7061:5eff:fe47:568e%AO2BbU3Q", By: "dnsmasq"},
		{Protocol: "udp", Port: 53, Address: "10.89.0.1", By: "dnsmasq"},
	}
	if ports := parseSockstat(output, 4242, "main"); !slices.Equal(ports, want) {
		t.Fatal("unexpected ports", ports)
	}
}

func TestCatalogLabelsSplitIntoNameAndHint(t *testing.T) {
	for label, want := range map[string][2]string{
		"Web UI (the editor runs in the browser; files open and save on the visitor's device)": {
			"Web UI", "The editor runs in the browser; files open and save on the visitor's device."},
		"Application data — SQLite database, stored documents, and config": {
			"Application data", "SQLite database, stored documents, and config."},
		"Address other cluster nodes reach this one on. The default suits a single node; clustering needs a routable address.": {
			"Address other cluster nodes reach this one on", "The default suits a single node; clustering needs a routable address."},
		"Cache Alpine's apk repositories under /alpine/: true or false.": {
			"Cache Alpine's apk repositories under /alpine/", "True or false."},
		"Address you open Vikunja at, e.g. http://192.168.1.10:3456/ (used in links and emails it sends)": {
			"Address you open Vikunja at", "E.g. http://192.168.1.10:3456/ (used in links and emails it sends)."},
		"Admin only. The connected app's name for RP_PAIRING_KEY. Default: Paired app": {
			"The connected app's name for RP_PAIRING_KEY", "Default: Paired app. Admin only."},
		"Config (config.toml) + rocksdb data store": {"Config (config.toml) + rocksdb data store", ""},
		"Specify one or multiple Chromium CLI flags, this string will be passed to the application in full.": {
			"SETTING", "Specify one or multiple Chromium CLI flags, this string will be passed to the application in full."},
		"Name ()": {"Name ()", ""},
		"":        {"SETTING", ""},
	} {
		if got, hint := catalogLabel("SETTING", label); got != want[0] || hint != want[1] {
			t.Errorf("catalogLabel(%q) = %q, %q; want %q, %q", label, got, hint, want[0], want[1])
		}
	}
}

func TestCatalogLabelsAreDistinct(t *testing.T) {
	variables := []containerServiceInterfaces.CatalogVariable{
		{Name: "PORT_143", Label: "IMAP4", Protocol: "tcp"},
		{Name: "PORT_993", Label: "IMAP4", Hint: "Implicit TLS.", Protocol: "tcp"},
		{Name: "PORT_443", Label: "HTTPS", Hint: "TCP + UDP/HTTP-3.", Protocol: "tcp"},
		{Name: "PORT_443_UDP", Label: "HTTPS", Hint: "TCP + UDP/HTTP-3.", Protocol: "udp"},
		{Name: "PORT_784_UDP", Label: "DNS-over-QUIC", Protocol: "udp"},
		{Name: "PORT_8853_UDP", Label: "DNS-over-QUIC", Protocol: "udp"},
		{Name: "TZ", Label: "Timezone"},
	}
	distinctLabels(variables, []string{"IMAP4", "IMAP4 (implicit TLS)", "HTTPS (TCP + UDP/HTTP-3)", "HTTPS (TCP + UDP/HTTP-3)",
		"DNS-over-QUIC", "DNS-over-QUIC", "Timezone"})
	var labels []string
	for _, variable := range variables {
		labels = append(labels, variable.Label)
	}
	want := []string{"IMAP4", "IMAP4 (implicit TLS)", "HTTPS (TCP)", "HTTPS (UDP)",
		"DNS-over-QUIC (PORT_784_UDP)", "DNS-over-QUIC (PORT_8853_UDP)", "Timezone"}
	if !slices.Equal(labels, want) || variables[1].Hint != "" || variables[2].Hint != "TCP + UDP/HTTP-3." {
		t.Fatal("unexpected labels", labels, variables[1].Hint, variables[2].Hint)
	}
}

func TestNATNetworksReapplyTheFirewall(t *testing.T) {
	service := newTestService(t)
	applied := 0
	service.ApplyFirewall = func() error { applied++; return nil }
	network, err := service.CreateNetwork(t.Context(), containerServiceInterfaces.NetworkRequest{Name: "web", Subnet: "192.0.2.0/24"})
	if err != nil || applied != 1 {
		t.Fatal("a new NAT network needs its firewall rules", applied, err)
	}
	if err := service.DeleteNetwork(t.Context(), network.ID); err != nil || applied != 2 {
		t.Fatal("a deleted NAT network's firewall rules must go", applied, err)
	}
}

func TestCatalogStacksRenderTheirContainers(t *testing.T) {
	service := newCatalogService(t)
	template, err := service.CatalogTemplate(t.Context(), "daemonless", "immich")
	if err != nil || !template.App.Stack || !slices.Equal(template.Containers, []string{"immich-database", "immich-machine-learning", "immich-redis", "immich-server"}) {
		t.Fatal("a stack lists its containers, each after those it needs", template.App.Stack, template.Containers, err)
	}
	for _, variable := range template.Variables {
		if variable.Name == "UPLOAD_LOCATION" && (variable.Default != "" || variable.Volume != "data") {
			t.Fatal("fjord's own directories are left for volumes", variable)
		}
	}

	plan, err := renderDefaults(t, service, "daemonless/immich", nil, nil)
	if err != nil || plan.Stack != "immich" || len(plan.Containers) != 4 {
		t.Fatal(plan, err)
	}
	containers := map[string]containerServiceInterfaces.CreateRequest{}
	for _, create := range plan.Containers {
		if create.Network != "immich" {
			t.Fatal("a stack's containers share its network", create.Name, create.Network)
		}
		for _, mount := range create.Mounts {
			if mount.Target == "/etc/localtime" || mount.Type == containerModels.MountVolume && mount.Owner != "1000:1000" {
				t.Fatal("files are not mounted, and volumes are the app's", create.Name, mount)
			}
		}
		containers[create.Name] = create
	}
	server, database := envOf(containers["immich-server"]), envOf(containers["immich-database"])
	if server["DB_HOSTNAME"] != "database" || server["REDIS_HOSTNAME"] != "redis" ||
		server["IMMICH_MACHINE_LEARNING_URL"] != "http://immich-machine-learning:3003" {
		t.Fatal("containers find each other by service name, which renaming them keeps", server)
	}
	if create := containers["immich-database"]; create.Service != "database" || create.StackTitle != "Immich" {
		t.Fatal("stack members keep their service and the stack its title", create.Service, create.StackTitle)
	}
	if server["DB_PASSWORD"] == "" || server["DB_PASSWORD"] != database["POSTGRES_PASSWORD"] {
		t.Fatal("the server is given every setting, as its .env, and shares the generated password", server["DB_PASSWORD"], database["POSTGRES_PASSWORD"])
	}
	web := containerModels.Port{Protocol: "tcp", HostPort: 2283, ContainerPort: 2283}
	if main := containers["immich-server"]; main.WebPort != 2283 || !slices.Equal(main.Ports, []containerModels.Port{web}) ||
		len(containers["immich-redis"].Ports) != 0 || main.Description == containers["immich-redis"].Description {
		t.Fatal("the server is the one users reach, its web UI published", main.WebPort, main.Ports)
	}

	plan, err = renderDefaults(t, service, "daemonless/immich", nil, map[string]string{"machine_learning": "off", "public_proxy": "on"})
	if err != nil || len(plan.Containers) != 4 {
		t.Fatal("options drop and add containers", plan, err)
	}
	names := []string{}
	for _, create := range plan.Containers {
		names = append(names, create.Name)
		if create.Name == "immich-server" && envOf(create)["IMMICH_MACHINE_LEARNING_ENABLED"] != "false" {
			t.Fatal("an option's settings reach the app", envOf(create))
		}
	}
	if !slices.Equal(names, []string{"immich-database", "immich-redis", "immich-server", "immich-public-proxy"}) {
		t.Fatal("unexpected containers", names)
	}
	if proxy := envOf(plan.Containers[3]); proxy["IMMICH_URL"] != "http://immich-server:2283" {
		t.Fatal("an added container finds the app by name", proxy)
	}
}

func TestStacksTakeFreeIDsAndShareANetwork(t *testing.T) {
	service := newTestService(t)
	ctx := t.Context()
	if err := service.DB.Create(&containerModels.Container{OCID: 301, Name: "taken", Pool: "tank", Image: "alpine", RuntimeID: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	request := containerServiceInterfaces.StackRequest{Stack: "web", Pool: "tank", OCID: 300, StartAtBoot: true,
		Containers: []containerServiceInterfaces.CreateRequest{
			{Name: "web-db", Spec: containerServiceInterfaces.Spec{Image: "postgres", OS: "linux"}},
			{Name: "web", Spec: containerServiceInterfaces.Spec{Image: "nginx", OS: "linux"}},
		}}
	prepared, err := service.PrepareStack(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for i, ocid := range []uint{300, 302} {
		create := prepared.Containers[i]
		if create.OCID != ocid || create.Pool != "tank" || create.Network != "web" || create.Stack != "web" || !create.StartAtBoot {
			t.Fatal("a stack's containers take free IDs in turn, on its pool and network", create)
		}
	}
	network, err := service.networkByName(ctx, "web")
	if err != nil || network.SwitchID != nil {
		t.Fatal("the stack gets a NAT network of its own", network, err)
	}

	member := containerModels.Container{OCID: 300, Name: "web-db", Pool: "tank", Image: "postgres", RuntimeID: "y", Stack: "web", NetworkID: &network.ID}
	if err := service.DB.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareStack(ctx, request); err == nil || !strings.Contains(err.Error(), "catalog_stack_exists") {
		t.Fatal("a stack is created once", err)
	}

	if err := service.RenameStack(ctx, "web", "  Web App "); err != nil {
		t.Fatal(err)
	}
	var titles []string
	if err := service.DB.Model(&containerModels.Container{}).Order("oc_id").Pluck("stack_title", &titles).Error; err != nil ||
		!slices.Equal(titles, []string{"Web App", ""}) {
		t.Fatal("renaming a stack titles its members only", titles, err)
	}
	if err := service.RenameStack(ctx, "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("only existing stacks are renamed", err)
	}
	if err := service.RenameStack(ctx, "", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal("containers outside stacks have no stack to rename", err)
	}
}
