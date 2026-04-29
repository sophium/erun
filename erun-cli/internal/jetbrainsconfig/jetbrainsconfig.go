package jetbrainsconfig

import (
	"crypto/sha1"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type ProjectEntry struct {
	ConfigID       string
	HostAlias      string
	User           string
	IdentityFile   string
	ProjectPath    string
	Port           int
	ProductCode    string
	TimestampMilli int64
}

type RecentProject struct {
	ConfigID       string
	ProjectPath    string
	ProductCode    string
	LatestUsedIDE  RecentProjectIDE
	TimestampMilli string
}

type RecentProjectIDE struct {
	BuildNumber string
	PathToIDE   string
	ProductCode string
}

func StableConfigID(hostAlias string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(hostAlias)))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

func UpsertOptionsFiles(optionsDir string, entry ProjectEntry) error {
	optionsDir = filepath.Clean(strings.TrimSpace(optionsDir))
	if optionsDir == "" {
		return fmt.Errorf("JetBrains options directory is required")
	}
	entry, err := normalizeProjectEntry(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(optionsDir, 0o700); err != nil {
		return err
	}

	if err := upsertSSHConfigsFile(filepath.Join(optionsDir, "sshConfigs.xml"), entry); err != nil {
		return err
	}
	if err := upsertSSHRecentConnectionsHostFile(filepath.Join(optionsDir, "sshRecentConnectionsHost.xml"), entry); err != nil {
		return err
	}
	if err := upsertSSHRecentConnectionsFile(filepath.Join(optionsDir, "sshRecentConnections.v2.xml"), entry); err != nil {
		return err
	}
	return nil
}

func normalizeProjectEntry(entry ProjectEntry) (ProjectEntry, error) {
	if err := validateProjectEntry(entry); err != nil {
		return ProjectEntry{}, err
	}
	if strings.TrimSpace(entry.ProductCode) == "" {
		entry.ProductCode = "IU"
	}
	return entry, nil
}

func validateProjectEntry(entry ProjectEntry) error {
	if strings.TrimSpace(entry.ConfigID) == "" {
		return fmt.Errorf("JetBrains config ID is required")
	}
	if strings.TrimSpace(entry.HostAlias) == "" {
		return fmt.Errorf("JetBrains host alias is required")
	}
	if strings.TrimSpace(entry.User) == "" {
		return fmt.Errorf("JetBrains SSH user is required")
	}
	if strings.TrimSpace(entry.ProjectPath) == "" {
		return fmt.Errorf("JetBrains project path is required")
	}
	return nil
}

func FindRecentProject(optionsDir string, configID string, projectPath string) (RecentProject, bool, error) {
	optionsDir = filepath.Clean(strings.TrimSpace(optionsDir))
	configID = strings.TrimSpace(configID)
	projectPath = strings.TrimSpace(projectPath)
	if optionsDir == "" {
		return RecentProject{}, false, fmt.Errorf("JetBrains options directory is required")
	}
	if configID == "" {
		return RecentProject{}, false, fmt.Errorf("JetBrains config ID is required")
	}
	if projectPath == "" {
		return RecentProject{}, false, fmt.Errorf("JetBrains project path is required")
	}

	doc := sshRecentConnectionsApplication{}
	if err := readXMLFile(filepath.Join(optionsDir, "sshRecentConnections.v2.xml"), &doc); err != nil {
		return RecentProject{}, false, err
	}
	for _, connection := range doc.Component.Connections.List.States {
		if connection.ConfigID() != configID {
			continue
		}
		for _, project := range connection.Projects() {
			if project.ProjectPath() != projectPath {
				continue
			}
			return RecentProject{
				ConfigID:       configID,
				ProjectPath:    project.ProjectPath(),
				ProductCode:    project.OptionValue("productCode"),
				LatestUsedIDE:  project.LatestUsedIDE(),
				TimestampMilli: project.OptionValue("date"),
			}, true, nil
		}
	}
	return RecentProject{}, false, nil
}

func ClearRecentProjectLatestUsedIDE(optionsDir string, configID string, projectPath string) (bool, error) {
	optionsDir = filepath.Clean(strings.TrimSpace(optionsDir))
	configID = strings.TrimSpace(configID)
	projectPath = strings.TrimSpace(projectPath)
	if optionsDir == "" {
		return false, fmt.Errorf("JetBrains options directory is required")
	}
	if configID == "" {
		return false, fmt.Errorf("JetBrains config ID is required")
	}
	if projectPath == "" {
		return false, fmt.Errorf("JetBrains project path is required")
	}

	path := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	doc := sshRecentConnectionsApplication{}
	if err := readXMLFile(path, &doc); err != nil {
		return false, err
	}
	for i := range doc.Component.Connections.List.States {
		if doc.Component.Connections.List.States[i].ConfigID() != configID {
			continue
		}
		projects := doc.Component.Connections.List.States[i].ProjectStates()
		for j := range projects {
			project := &projects[j]
			if project.ProjectPath() != projectPath {
				continue
			}
			if !project.ClearLatestUsedIDE() {
				return false, nil
			}
			return true, writeXMLFile(path, doc)
		}
	}
	return false, nil
}

func upsertSSHConfigsFile(path string, entry ProjectEntry) error {
	doc := sshConfigsApplication{
		Component: sshConfigsComponent{
			Name: "SshConfigs",
		},
	}
	if err := readXMLFile(path, &doc); err != nil {
		return err
	}
	doc.Component.Name = "SshConfigs"

	found := false
	for i := range doc.Component.Configs.Entries {
		current := doc.Component.Configs.Entries[i]
		if current.ID == entry.ConfigID || current.Host == entry.HostAlias {
			doc.Component.Configs.Entries[i] = sshConfigEntry{
				Host:             entry.HostAlias,
				ID:               entry.ConfigID,
				KeyPath:          entry.IdentityFile,
				Port:             entry.Port,
				NameFormat:       "DESCRIPTIVE",
				Username:         entry.User,
				UseOpenSSHConfig: true,
			}
			found = true
			break
		}
	}
	if !found {
		doc.Component.Configs.Entries = append(doc.Component.Configs.Entries, sshConfigEntry{
			Host:             entry.HostAlias,
			ID:               entry.ConfigID,
			KeyPath:          entry.IdentityFile,
			Port:             entry.Port,
			NameFormat:       "DESCRIPTIVE",
			Username:         entry.User,
			UseOpenSSHConfig: true,
		})
	}
	slices.SortFunc(doc.Component.Configs.Entries, func(a, b sshConfigEntry) int {
		return strings.Compare(a.Host, b.Host)
	})
	return writeXMLFile(path, doc)
}

func upsertSSHRecentConnectionsHostFile(path string, entry ProjectEntry) error {
	doc := sshHostStorageApplication{
		Component: sshHostStorageComponent{
			Name: "SshHostStorage",
			ConfigIDs: configIDsOption{
				Name: "configIds",
			},
		},
	}
	if err := readXMLFile(path, &doc); err != nil {
		return err
	}
	doc.Component.Name = "SshHostStorage"
	doc.Component.ConfigIDs.Name = "configIds"

	found := false
	for _, option := range doc.Component.ConfigIDs.List.Options {
		if option.Value == entry.ConfigID {
			found = true
			break
		}
	}
	if !found {
		doc.Component.ConfigIDs.List.Options = append(doc.Component.ConfigIDs.List.Options, valueOption{Value: entry.ConfigID})
	}
	slices.SortFunc(doc.Component.ConfigIDs.List.Options, func(a, b valueOption) int {
		return strings.Compare(a.Value, b.Value)
	})
	return writeXMLFile(path, doc)
}

func upsertSSHRecentConnectionsFile(path string, entry ProjectEntry) error {
	doc := sshRecentConnectionsApplication{
		Component: sshRecentConnectionsComponent{
			Name: "SshLocalRecentConnectionsManager",
			Connections: recentConnectionsOption{
				Name: "connections",
			},
		},
	}
	if err := readXMLFile(path, &doc); err != nil {
		return err
	}
	doc.Component.Name = "SshLocalRecentConnectionsManager"
	doc.Component.Connections.Name = "connections"

	projectOption := recentProjectState{
		Options: []recentProjectOption{
			{Name: "date", Value: fmt.Sprintf("%d", entry.TimestampMilli)},
			{Name: "productCode", Value: entry.ProductCode},
			{Name: "projectPath", Value: entry.ProjectPath},
		},
	}

	foundConnection := false
	for i := range doc.Component.Connections.List.States {
		configID := doc.Component.Connections.List.States[i].ConfigID()
		if configID != entry.ConfigID {
			continue
		}
		foundConnection = true
		doc.Component.Connections.List.States[i].UpsertProject(projectOption, entry.ProjectPath)
		break
	}
	if !foundConnection {
		state := localRecentConnectionState{
			Options: []recentConnectionOption{
				{Name: "configId", Value: entry.ConfigID},
				{
					Name: "projects",
					List: &recentProjectsList{
						Projects: []recentProjectState{projectOption},
					},
				},
			},
		}
		doc.Component.Connections.List.States = append(doc.Component.Connections.List.States, state)
	}
	slices.SortFunc(doc.Component.Connections.List.States, func(a, b localRecentConnectionState) int {
		return strings.Compare(a.ConfigID(), b.ConfigID())
	})
	return writeXMLFile(path, doc)
}

func readXMLFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return xml.Unmarshal(data, target)
}

func writeXMLFile(path string, doc any) error {
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

type sshConfigsApplication struct {
	XMLName   xml.Name            `xml:"application"`
	Component sshConfigsComponent `xml:"component"`
}

type sshConfigsComponent struct {
	Name    string         `xml:"name,attr"`
	Configs sshConfigsList `xml:"configs"`
}

type sshConfigsList struct {
	Entries []sshConfigEntry `xml:"sshConfig"`
}

type sshConfigEntry struct {
	Host             string `xml:"host,attr"`
	ID               string `xml:"id,attr"`
	KeyPath          string `xml:"keyPath,attr,omitempty"`
	Port             int    `xml:"port,attr"`
	NameFormat       string `xml:"nameFormat,attr,omitempty"`
	Username         string `xml:"username,attr"`
	UseOpenSSHConfig bool   `xml:"useOpenSSHConfig,attr"`
}

type sshHostStorageApplication struct {
	XMLName   xml.Name                `xml:"application"`
	Component sshHostStorageComponent `xml:"component"`
}

type sshHostStorageComponent struct {
	Name      string          `xml:"name,attr"`
	ConfigIDs configIDsOption `xml:"option"`
}

type configIDsOption struct {
	Name string          `xml:"name,attr"`
	List valueOptionList `xml:"list"`
}

type valueOptionList struct {
	Options []valueOption `xml:"option"`
}

type valueOption struct {
	Value string `xml:"value,attr"`
}

type sshRecentConnectionsApplication struct {
	XMLName   xml.Name                      `xml:"application"`
	Component sshRecentConnectionsComponent `xml:"component"`
}

type sshRecentConnectionsComponent struct {
	Name        string                  `xml:"name,attr"`
	Connections recentConnectionsOption `xml:"option"`
}

type recentConnectionsOption struct {
	Name string              `xml:"name,attr"`
	List localConnectionList `xml:"list"`
}

type localConnectionList struct {
	States []localRecentConnectionState `xml:"LocalRecentConnectionState"`
}

type localRecentConnectionState struct {
	Options []recentConnectionOption `xml:"option"`
}

func (state localRecentConnectionState) ConfigID() string {
	for _, option := range state.Options {
		if option.Name == "configId" {
			return option.Value
		}
	}
	return ""
}

func (state *localRecentConnectionState) UpsertProject(project recentProjectState, projectPath string) {
	for i := range state.Options {
		if state.Options[i].Name != "projects" {
			continue
		}
		if state.Options[i].List == nil {
			state.Options[i].List = &recentProjectsList{}
		}
		for j := range state.Options[i].List.Projects {
			if state.Options[i].List.Projects[j].ProjectPath() == projectPath {
				state.Options[i].List.Projects[j].MergeMetadata(project)
				return
			}
		}
		state.Options[i].List.Projects = append(state.Options[i].List.Projects, project)
		slices.SortFunc(state.Options[i].List.Projects, func(a, b recentProjectState) int {
			return strings.Compare(a.ProjectPath(), b.ProjectPath())
		})
		return
	}
	state.Options = append(state.Options, recentConnectionOption{
		Name: "projects",
		List: &recentProjectsList{Projects: []recentProjectState{project}},
	})
}

type recentConnectionOption struct {
	Name  string              `xml:"name,attr"`
	Value string              `xml:"value,attr,omitempty"`
	List  *recentProjectsList `xml:"list,omitempty"`
}

type recentProjectsList struct {
	Projects []recentProjectState `xml:"RecentProjectState"`
}

type recentProjectState struct {
	Options []recentProjectOption `xml:"option"`
}

func (state recentProjectState) ProjectPath() string {
	for _, option := range state.Options {
		if option.Name == "projectPath" {
			return option.Value
		}
	}
	return ""
}

func (state localRecentConnectionState) Projects() []recentProjectState {
	for _, option := range state.Options {
		if option.Name == "projects" && option.List != nil {
			return option.List.Projects
		}
	}
	return nil
}

func (state *localRecentConnectionState) ProjectStates() []recentProjectState {
	for i := range state.Options {
		if state.Options[i].Name == "projects" && state.Options[i].List != nil {
			return state.Options[i].List.Projects
		}
	}
	return nil
}

type recentProjectOption struct {
	Name          string                     `xml:"name,attr"`
	Value         string                     `xml:"value,attr,omitempty"`
	LatestUsedIDE *recentProjectInstalledIDE `xml:"RecentProjectInstalledIde,omitempty"`
}

type recentProjectInstalledIDE struct {
	Options []installedIDEOption `xml:"option"`
}

func (state recentProjectState) OptionValue(name string) string {
	for _, option := range state.Options {
		if option.Name == name {
			return option.Value
		}
	}
	return ""
}

func (state recentProjectState) LatestUsedIDE() RecentProjectIDE {
	for _, option := range state.Options {
		if option.Name != "latestUsedIde" || option.LatestUsedIDE == nil {
			continue
		}
		return RecentProjectIDE{
			BuildNumber: option.LatestUsedIDE.OptionValue("buildNumber"),
			PathToIDE:   option.LatestUsedIDE.OptionValue("pathToIde"),
			ProductCode: option.LatestUsedIDE.OptionValue("productCode"),
		}
	}
	return RecentProjectIDE{}
}

func (state *recentProjectState) MergeMetadata(project recentProjectState) {
	state.upsertOptionValue("date", project.OptionValue("date"))
	state.upsertOptionValue("productCode", project.OptionValue("productCode"))
	state.upsertOptionValue("projectPath", project.ProjectPath())
}

func (state *recentProjectState) ClearLatestUsedIDE() bool {
	for i := range state.Options {
		if state.Options[i].Name != "latestUsedIde" {
			continue
		}
		state.Options = append(state.Options[:i], state.Options[i+1:]...)
		return true
	}
	return false
}

func (state *recentProjectState) upsertOptionValue(name string, value string) {
	for i := range state.Options {
		if state.Options[i].Name == name {
			state.Options[i].Value = value
			return
		}
	}
	state.Options = append(state.Options, recentProjectOption{Name: name, Value: value})
}

type installedIDEOption struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr,omitempty"`
}

func (ide recentProjectInstalledIDE) OptionValue(name string) string {
	for _, option := range ide.Options {
		if option.Name == name {
			return option.Value
		}
	}
	return ""
}
