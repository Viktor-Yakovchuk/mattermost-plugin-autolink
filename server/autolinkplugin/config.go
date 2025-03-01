package autolinkplugin

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-autolink/server/autolink"
)

// Config from config.json
type Config struct {
	EnableAdminCommand bool                `json:"enableadmincommand"`
	EnableOnUpdate     bool                `json:"enableonupdate"`
	PluginAdmins       string              `json:"pluginadmins"`
	Links              []autolink.Autolink `json:"links"`

	// AdminUserIds is a set of UserIds that are permitted to perform
	// administrative operations on the plugin configuration (i.e. plugin
	// admins). On each configuration change the contents of PluginAdmins
	// config field is parsed into this field.
	AdminUserIds map[string]struct{} `json:"-"`
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	var c Config
	if err := p.API.LoadPluginConfiguration(&c); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}

	for i := range c.Links {
		if err := c.Links[i].Compile(); err != nil {
			p.API.LogError("Error creating autolinker", "link", c.Links[i], "error", err.Error())
		}
	}

	// Plugin admin UserId parsing and validation errors are
	// not fatal, if everything fails only sysadmin will be able to manage the
	// config which is still OK
	c.parsePluginAdminList(p.API)

	p.UpdateConfig(func(conf *Config) {
		*conf = c
	})

	go func() {
		if c.EnableAdminCommand {
			_ = p.API.RegisterCommand(&model.Command{
				Trigger:          "autolink",
				DisplayName:      "Autolink",
				Description:      "Autolink administration.",
				AutoComplete:     true,
				AutoCompleteDesc: "Available commands: add, delete, disable, enable, list, set, test",
				AutoCompleteHint: "[command]",
				AutocompleteData: getAutoCompleteData(),
			})
		} else {
			_ = p.API.UnregisterCommand("", "autolink")
		}
	}()

	return nil
}

func getAutoCompleteData() *model.AutocompleteData {
	autolink := model.NewAutocompleteData("autolink", "[command]",
		"Available command : add, delete, disable, enable, list, set, test")

	add := model.NewAutocompleteData("add", "",
		"Add a new link with a given name")
	add.AddTextArgument("Name for a new link", "[name]", "")
	autolink.AddCommand(add)

	delete := model.NewAutocompleteData("delete", "",
		"Delete a link with a given name")
	delete.AddTextArgument("Name of the link to delete", "[name]", "")
	autolink.AddCommand(delete)

	disable := model.NewAutocompleteData("disable", "",
		"Disable a link with a given name")
	disable.AddTextArgument("Name of the link to disable", "[name]", "")
	autolink.AddCommand(disable)

	enable := model.NewAutocompleteData("enable", "",
		"Enable a link with a given name")
	enable.AddTextArgument("Name of the link to enable", "[name]", "")
	autolink.AddCommand(enable)

	list := model.NewAutocompleteData("list", "",
		"List all configured links")
	list.AddStaticListArgument("List the link which match with the given condition",
		false, []model.AutocompleteListItem{
			{
				HelpText: "If `name` of a link is provided, it will only list a configuration of `name` link ",
				Hint:     "(optional)",
				Item:     "[name]",
			},
			{
				HelpText: "List configuration of link matched with the given template",
				Hint:     "(optional)",
				Item:     "Template",
			},
			{
				HelpText: "List configuration of link matched with the given pattern",
				Hint:     "(optional)",
				Item:     "Pattern",
			},
		})
	autolink.AddCommand(list)

	set := model.NewAutocompleteData("set", "",
		"Set a field of a link with a given value")
	set.AddTextArgument("Name of a link to set", "[name]", "")
	set.AddStaticListArgument("A name of a field to set a value", false,
		[]model.AutocompleteListItem{
			{
				HelpText: "Set the `Template` field",
				Hint:     "",
				Item:     "Template",
			},
			{
				HelpText: "Set the `Pattern` field",
				Hint:     "",
				Item:     "Pattern",
			},
			{
				HelpText: "If true uses the \\b word boundaries",
				Hint:     "",
				Item:     "WordMatch",
			},
			{
				HelpText: "If true applies changes to posts created by bot accounts.",
				Hint:     "",
				Item:     "ProcessBotPosts",
			},
			{
				HelpText: "team/channel the autolink applies to",
				Hint:     "",
				Item:     "Scope",
			},
		})
	autolink.AddCommand(set)

	test := model.NewAutocompleteData("test", "",
		"Test a link on the text provided")
	test.AddTextArgument("Name of a link to test with", "[name]", "")
	test.AddTextArgument("Sample text which the link applies", "[sample text]", "")
	autolink.AddCommand(test)

	help := model.NewAutocompleteData("help", "", "Autolink plugin slash command help")
	autolink.AddCommand(help)

	return autolink
}

func (p *Plugin) getConfig() *Config {
	p.confLock.RLock()
	defer p.confLock.RUnlock()

	return p.conf
}

func (p *Plugin) GetLinks() []autolink.Autolink {
	p.confLock.RLock()
	defer p.confLock.RUnlock()

	return p.conf.Links
}

func (p *Plugin) SaveLinks(links []autolink.Autolink) error {
	p.UpdateConfig(func(conf *Config) {
		conf.Links = links
	})

	configMap, err := p.getConfig().ToMap()
	if err != nil {
		return errors.Wrap(err, "unable convert config to map")
	}

	appErr := p.API.SavePluginConfig(configMap)
	if appErr != nil {
		return errors.Wrap(appErr, "unable to save links")
	}

	return nil
}

func (p *Plugin) UpdateConfig(f func(conf *Config)) {
	p.confLock.Lock()
	defer p.confLock.Unlock()

	f(p.conf)
}

// ToConfig marshals Config into a tree of map[string]interface{} to pass down
// to p.API.SavePluginConfig, otherwise RPC/gob barfs at the unknown type.
func (conf *Config) ToMap() (map[string]interface{}, error) {
	var out map[string]interface{}
	data, err := json.Marshal(conf)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Sorted returns a clone of the Config, with links sorted alphabetically
func (conf *Config) Sorted() *Config {
	sorted := conf
	sorted.Links = append([]autolink.Autolink{}, conf.Links...)
	sort.Slice(conf.Links, func(i, j int) bool {
		return strings.Compare(conf.Links[i].DisplayName(), conf.Links[j].DisplayName()) < 0
	})
	return conf
}

// parsePluginAdminList parses the contents of PluginAdmins config field
func (conf *Config) parsePluginAdminList(api plugin.API) {
	conf.AdminUserIds = make(map[string]struct{}, len(conf.PluginAdmins))

	if len(conf.PluginAdmins) == 0 {
		// There were no plugin admin users defined
		return
	}

	userIDs := strings.Split(conf.PluginAdmins, ",")
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		// Let's verify that the given user really exists
		_, appErr := api.GetUser(userID)
		if appErr != nil {
			api.LogWarn("Error occurred while verifying userID", "userID", userID, "error", appErr)
		} else {
			conf.AdminUserIds[userID] = struct{}{}
		}
	}
}
