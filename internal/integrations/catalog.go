package integrations

import "fmt"

// KindInfo describes one integration for the Integrations screen: its fixed name, how it authenticates, what an assignment to
// a project asks for, and which API (and version) Drydock calls. The screen is built from it, so the core stays the one place
// that knows the vendors.
type KindInfo struct {
	Kind       string       `json:"kind"`
	Label      string       `json:"label"` // the vendor's name: fixed, never editable
	Blurb      string       `json:"blurb"`
	Items      string       `json:"items"` // what it imports, in words
	API        string       `json:"api"`
	APIVersion string       `json:"apiVersion"`
	SDK        string       `json:"sdk"` // the official SDK situation and why Drydock calls the API directly
	RateLimit  string       `json:"rateLimit"`
	Docs       []DocLink    `json:"docs"`
	Auth       []AuthMethod `json:"auth"`
	Settings   []Setting    `json:"settings"`
	Ops        []OpInfo     `json:"ops"`
	// NoSignIn says, in one sentence, why there is no Sign in button for this kind (Notion, Aikido); "" when it has one.
	NoSignIn string `json:"noSignIn,omitempty"`
}

// DocLink is a vendor documentation page Drydock follows.
type DocLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// AuthMethod is one way to connect.
type AuthMethod struct {
	ID        string      `json:"id"` // api_key | oauth | client_credentials | gh
	Label     string      `json:"label"`
	Fields    []AuthField `json:"fields,omitempty"`
	Available bool        `json:"available"`
	Note      string      `json:"note,omitempty"`
	Flow      string      `json:"flow,omitempty"` // oauth: pkce | device
	// OAuthApp (oauth) is the app this sign-in uses, or where to register one (Kinds fills it in; oauthapps.go).
	OAuthApp *OAuthAppInfo `json:"oauthApp,omitempty"`
}

// AuthField is one input of an AuthMethod.
type AuthField struct {
	Key     string   `json:"key"` // token | clientId | clientSecret | region | baseUrl
	Label   string   `json:"label"`
	Secret  bool     `json:"secret,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	Options []Choice `json:"options,omitempty"`
	// Advanced fields are folded away (an API URL for a self-hosted server).
	Advanced bool `json:"advanced,omitempty"`
}

// Setting is one field of an assignment (the project's remote scope).
type Setting struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Hint     string `json:"hint,omitempty"`
	Required bool   `json:"required,omitempty"`
	Choices  bool   `json:"choices,omitempty"` // the values can be listed from the vendor's API (integration.choices)
}

var apiURLField = AuthField{Key: "baseUrl", Label: "API URL", Advanced: true, Hint: "Only for a self-hosted server or a test server; leave empty for the vendor's own API."}

// Catalog lists the integrations in display order. oauth tells which kinds can sign in in this build.
func Catalog(oauth func(kind string) bool) []KindInfo {
	if oauth == nil {
		oauth = func(string) bool { return false }
	}
	regions := []Choice{{Value: "eu", Label: "Europe (app.aikido.dev)"}, {Value: "us", Label: "United States (app.us.aikido.dev)"},
		{Value: "me", Label: "Middle East (app.me.aikido.dev)"}, {Value: "au", Label: "Australia (app.au.aikido.dev)"}}
	notReg := "Sign in needs an OAuth app registered with %s: register one and paste its client id here, or paste a key instead."
	return []KindInfo{
		{
			Kind: KindLinear, Label: "Linear", Blurb: "Issues and projects", Items: "issues",
			API: "Linear GraphQL API (https://api.linear.app/graphql)", APIVersion: "GraphQL schema of @linear/sdk 99.0.0 (unversioned API)",
			SDK:       "Linear's official SDK is TypeScript (@linear/sdk). There is no official Go SDK, so Drydock sends GraphQL over HTTPS that follows the SDK's schema.",
			RateLimit: limitLinearKey.Note + " (" + limitLinearOAuth.Note + ")",
			Docs: []DocLink{{"GraphQL API", "https://linear.app/developers/graphql"}, {"Rate limiting", "https://linear.app/developers/rate-limiting"},
				{"OAuth 2.0", "https://linear.app/developers/oauth-2-0-authentication"}, {"Pagination", "https://linear.app/developers/pagination"}},
			Auth: []AuthMethod{
				{ID: AuthAPIKey, Label: "Personal API key", Available: true, Note: "Linear: Settings > Security & access > Personal API keys.",
					Fields: []AuthField{{Key: "token", Label: "API key", Secret: true, Hint: "Starts with lin_api_. Stored in the credential store, never shown again."}, apiURLField}},
				{ID: AuthOAuth, Label: "Sign in with Linear", Flow: "pkce", Available: oauth(KindLinear), Note: noteIf(!oauth(KindLinear), notReg, "Linear")},
			},
			Settings: []Setting{{Key: "team", Label: "Team", Hint: "Only this team's issues (the team key, such as ENG). Empty: every team the key can see.", Choices: true},
				{Key: "project", Label: "Linear project", Hint: "Only issues of this Linear project.", Choices: true}},
			Ops: linearAdapter{}.Ops(),
		},
		{
			Kind: KindNotion, Label: "Notion", Blurb: "Pages as requirements text", Items: "pages",
			API: "Notion REST API (https://api.notion.com/v1)", APIVersion: "Notion-Version " + NotionVersion,
			SDK:       "Notion's official SDK is JavaScript (@notionhq/client). There is no official Go SDK, so Drydock calls the REST API, following the SDK's endpoint definitions, with the data sources API.",
			RateLimit: limitNotion.Note,
			Docs: []DocLink{{"Versioning", "https://developers.notion.com/reference/versioning"}, {"Upgrade guide 2026-03-11", "https://developers.notion.com/guides/get-started/upgrade-guide-2026-03-11"},
				{"Request limits", "https://developers.notion.com/reference/request-limits"}, {"Query a data source", "https://developers.notion.com/reference/query-a-data-source"}},
			Auth: []AuthMethod{
				{ID: AuthAPIKey, Label: "Internal integration secret", Available: true,
					Note:   "Notion: Settings > Connections > Develop or manage integrations; then share the pages or databases with the integration.",
					Fields: []AuthField{{Key: "token", Label: "Integration secret", Secret: true, Hint: "Starts with ntn_. Stored in the credential store, never shown again."}, apiURLField}},
			},
			NoSignIn: "There is no Sign in button for Notion: its public OAuth needs a client secret at the token exchange, which a desktop app cannot keep, so Notion connects with an internal integration secret.",
			Settings: []Setting{{Key: "data_source", Label: "Data source", Hint: "A database's data source shared with the integration. Empty: search the shared pages.", Choices: true},
				{Key: "query", Label: "Search text", Hint: "Without a data source: only pages whose title matches."}},
			Ops: notionAdapter{}.Ops(),
		},
		{
			Kind: KindSentry, Label: "Sentry", Blurb: "Errors and issues", Items: "issues",
			API: "Sentry web API (https://sentry.io/api/0/)", APIVersion: "v0",
			SDK:       "sentry-go is Sentry's SDK for sending errors, not a client of this API, and there is no official Go API client, so Drydock calls the REST API.",
			RateLimit: limitSentry.Note,
			Docs: []DocLink{{"Authentication", "https://docs.sentry.io/api/auth/"}, {"Pagination", "https://docs.sentry.io/api/pagination/"},
				{"Rate limits", "https://docs.sentry.io/api/ratelimits/"}, {"List an organization's issues", "https://docs.sentry.io/api/events/list-an-organizations-issues/"}},
			Auth: []AuthMethod{
				{ID: AuthAPIKey, Label: "Auth token", Available: true, Note: "A personal token (User settings > Personal Tokens) or an internal integration's token, with org:read, project:read and event:read (event:write to resolve).",
					Fields: []AuthField{{Key: "token", Label: "Auth token", Secret: true, Hint: "Stored in the credential store, never shown again."},
						{Key: "baseUrl", Label: "Sentry URL", Advanced: true, Hint: "Only for a self-hosted Sentry (https://sentry.example.com); empty for sentry.io."}}},
				{ID: AuthOAuth, Label: "Sign in with Sentry (device code)", Flow: "device", Available: oauth(KindSentry), Note: noteIf(!oauth(KindSentry), notReg, "Sentry")},
			},
			Settings: []Setting{{Key: "organization", Label: "Organization", Required: true, Choices: true},
				{Key: "project", Label: "Sentry project", Hint: "Empty: every project of the organization.", Choices: true},
				{Key: "query", Label: "Search query", Hint: "Sentry search syntax; default is:unresolved."}},
			Ops: sentryAdapter{}.Ops(),
		},
		{
			Kind: KindAikido, Label: "Aikido", Blurb: "Security findings", Items: "findings",
			API: "Aikido public REST API (/api/public/v1)", APIVersion: "v1",
			SDK:       "Aikido publishes no SDK; its public API takes OAuth 2.0 client credentials. Drydock calls the REST API directly.",
			RateLimit: limitAikido.Note,
			Docs:      []DocLink{{"API reference", "https://apidocs.aikido.dev/"}, {"Authorization", "https://apidocs.aikido.dev/reference/authorization"}, {"Export all issues", "https://apidocs.aikido.dev/reference/exportissues"}},
			Auth: []AuthMethod{
				{ID: AuthClientCredentials, Label: "REST API client credentials", Available: true, Note: "Aikido: Integrations > Public REST API (a workspace admin creates the client and chooses its scopes, such as issues:read).",
					Fields: []AuthField{{Key: "region", Label: "Region", Options: regions}, {Key: "clientId", Label: "Client ID"},
						{Key: "clientSecret", Label: "Client secret", Secret: true, Hint: "Stored in the credential store, never shown again."}, apiURLField}},
			},
			Settings: []Setting{{Key: "repo", Label: "Repository", Hint: "Only findings of this code repository. Empty: the whole workspace.", Choices: true}},
			Ops:      aikidoAdapter{}.Ops(),
			NoSignIn: "There is no Sign in button for Aikido: its API offers OAuth client credentials only (no sign-in for a person), so Aikido connects with the client ID and secret a workspace admin creates.",
		},
		{
			Kind: KindGitHub, Label: "GitHub", Blurb: "Issues of a repository", Items: "issues",
			API: "GitHub REST API through the GitHub CLI (gh api), or directly with a Sign in with GitHub", APIVersion: "REST API 2022-11-28",
			SDK:       "Drydock uses the GitHub CLI you signed in with (the Connections screen shows it) and stores no GitHub token; with a Sign in with GitHub (an OAuth app you register) it holds that sign-in's token in the credential store instead.",
			RateLimit: limitGitHub.Note,
			Docs:      []DocLink{{"List repository issues", "https://docs.github.com/en/rest/issues/issues#list-repository-issues"}, {"gh auth login", "https://cli.github.com/manual/gh_auth_login"}, {"Rate limits", limitGitHub.Doc}},
			Auth: []AuthMethod{{ID: AuthGH, Label: "Use the GitHub CLI sign-in", Available: true, Note: "Run `gh auth login` once; Drydock reuses that sign-in."},
				{ID: AuthOAuth, Label: "Sign in with GitHub (device code)", Flow: "device", Available: oauth(KindGitHub), Note: noteIf(!oauth(KindGitHub), notReg, "GitHub")}},
			Settings: []Setting{{Key: "repo", Label: "Repository", Hint: "owner/name. Empty: the project's own repository (its origin remote).", Choices: true},
				{Key: "labels", Label: "Labels", Hint: "Only issues with all of these labels (comma-separated)."}},
		},
	}
}

func noteIf(cond bool, format, arg string) string {
	if !cond {
		return ""
	}
	return fmt.Sprintf(format, arg)
}
