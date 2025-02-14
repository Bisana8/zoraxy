package dynamicproxy

import (
	_ "embed"
	"net"
	"net/http"
	"sync"

	"imuslab.com/zoraxy/mod/access"
	"imuslab.com/zoraxy/mod/dynamicproxy/dpcore"
	"imuslab.com/zoraxy/mod/dynamicproxy/loadbalance"
	"imuslab.com/zoraxy/mod/dynamicproxy/permissionpolicy"
	"imuslab.com/zoraxy/mod/dynamicproxy/redirection"
	"imuslab.com/zoraxy/mod/geodb"
	"imuslab.com/zoraxy/mod/info/logger"
	"imuslab.com/zoraxy/mod/statistic"
	"imuslab.com/zoraxy/mod/tlscert"
)

const (
	ProxyType_Root = 0
	ProxyType_Host = 1
	ProxyType_Vdir = 2
)

type ProxyHandler struct {
	Parent *Router
}

/* Router Object Options */
type RouterOption struct {
	HostUUID           string                    //The UUID of Zoraxy, use for heading mod
	HostVersion        string                    //The version of Zoraxy, use for heading mod
	Port               int                       //Incoming port
	UseTls             bool                      //Use TLS to serve incoming requsts
	ForceTLSLatest     bool                      //Force TLS1.2 or above
	NoCache            bool                      //Force set Cache-Control: no-store
	ListenOnPort80     bool                      //Enable port 80 http listener
	ForceHttpsRedirect bool                      //Force redirection of http to https endpoint
	TlsManager         *tlscert.Manager          //TLS manager for serving SAN certificates
	RedirectRuleTable  *redirection.RuleTable    //Redirection rules handler and table
	GeodbStore         *geodb.Store              //GeoIP resolver
	AccessController   *access.Controller        //Blacklist / whitelist controller
	StatisticCollector *statistic.Collector      //Statistic collector for storing stats on incoming visitors
	WebDirectory       string                    //The static web server directory containing the templates folder
	LoadBalancer       *loadbalance.RouteManager //Load balancer that handle load balancing of proxy target
	Logger             *logger.Logger            //Logger for reverse proxy requets
}

/* Router Object */
type Router struct {
	Option         *RouterOption
	ProxyEndpoints *sync.Map
	Running        bool
	Root           *ProxyEndpoint
	mux            http.Handler
	server         *http.Server
	tlsListener    net.Listener
	loadBalancer   *loadbalance.RouteManager //Load balancer routing manager
	routingRules   []*RoutingRule

	tlsRedirectStop  chan bool              //Stop channel for tls redirection server
	rateLimterStop   chan bool              //Stop channel for rate limiter
	rateLimitCounter RequestCountPerIpTable //Request counter for rate limter
}

/* Basic Auth Related Data structure*/
// Auth credential for basic auth on certain endpoints
type BasicAuthCredentials struct {
	Username     string
	PasswordHash string
}

// Auth credential for basic auth on certain endpoints
type BasicAuthUnhashedCredentials struct {
	Username string
	Password string
}

// Paths to exclude in basic auth enabled proxy handler
type BasicAuthExceptionRule struct {
	PathPrefix string
}

/* Custom Header Related Data structure */
// Header injection direction type
type HeaderDirection int

const (
	HeaderDirection_ZoraxyToUpstream   HeaderDirection = 0 //Inject (or remove) header to request out-going from Zoraxy to backend server
	HeaderDirection_ZoraxyToDownstream HeaderDirection = 1 //Inject (or remove) header to request out-going from Zoraxy to client (e.g. browser)
)

// User defined headers to add into a proxy endpoint
type UserDefinedHeader struct {
	Direction HeaderDirection
	Key       string
	Value     string
	IsRemove  bool //Instead of set, remove this key instead
}

/* Routing Rule Data Structures */

// A Virtual Directory endpoint, provide a subset of ProxyEndpoint for better
// program structure than directly using ProxyEndpoint
type VirtualDirectoryEndpoint struct {
	MatchingPath        string               //Matching prefix of the request path, also act as key
	Domain              string               //Domain or IP to proxy to
	RequireTLS          bool                 //Target domain require TLS
	SkipCertValidations bool                 //Set to true to accept self signed certs
	Disabled            bool                 //If the rule is enabled
	proxy               *dpcore.ReverseProxy `json:"-"`
	parent              *ProxyEndpoint       `json:"-"`
}

// A proxy endpoint record, a general interface for handling inbound routing
type ProxyEndpoint struct {
	ProxyType            int                     //The type of this proxy, see const def
	RootOrMatchingDomain string                  //Matching domain for host, also act as key
	MatchingDomainAlias  []string                //A list of domains that alias to this rule
	ActiveOrigins        []*loadbalance.Upstream //Activated Upstream or origin servers IP or domain to proxy to
	InactiveOrigins      []*loadbalance.Upstream //Disabled Upstream or origin servers IP or domain to proxy to
	UseStickySession     bool                    //Use stick session for load balancing
	UseActiveLoadBalance bool                    //Use active loadbalancing, default passive
	Disabled             bool                    //If the rule is disabled

	//Inbound TLS/SSL Related
	BypassGlobalTLS bool //Bypass global TLS setting options if TLS Listener enabled (parent.tlsListener != nil)

	//Virtual Directories
	VirtualDirectories []*VirtualDirectoryEndpoint

	//Custom Headers
	UserDefinedHeaders           []*UserDefinedHeader                //Custom headers to append when proxying requests from this endpoint
	HSTSMaxAge                   int64                               //HSTS max age, set to 0 for disable HSTS headers
	EnablePermissionPolicyHeader bool                                //Enable injection of permission policy header
	PermissionPolicy             *permissionpolicy.PermissionsPolicy //Permission policy header
	DisableHopByHopHeaderRemoval bool                                //TODO: Do not remove hop-by-hop headers

	//Authentication
	RequireBasicAuth        bool                      //Set to true to request basic auth before proxy
	BasicAuthCredentials    []*BasicAuthCredentials   //Basic auth credentials
	BasicAuthExceptionRules []*BasicAuthExceptionRule //Path to exclude in a basic auth enabled proxy target

	// Rate Limiting
	RequireRateLimit bool
	RateLimit        int64 // Rate limit in requests per second

	//Access Control
	AccessFilterUUID string //Access filter ID

	//Fallback routing logic (Special Rule Sets Only)
	DefaultSiteOption int    //Fallback routing logic options
	DefaultSiteValue  string //Fallback routing target, optional

	//Internal Logic Elements
	parent *Router `json:"-"`
}

/*
	Routing type specific interface
	These are options that only avaible for a specific interface
	when running, these are converted into "ProxyEndpoint" objects
	for more generic routing logic
*/

// Root options are those that are required for reverse proxy handler to work
const (
	DefaultSite_InternalStaticWebServer = 0
	DefaultSite_ReverseProxy            = 1
	DefaultSite_Redirect                = 2
	DefaultSite_NotFoundPage            = 3
)

/*
Web Templates
*/
var (
	//go:embed templates/forbidden.html
	page_forbidden []byte
	//go:embed templates/hosterror.html
	page_hosterror []byte
)
