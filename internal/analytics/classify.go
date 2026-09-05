package analytics

import (
	"net"
	"strings"
)

// Class splits traffic three ways so "how many people came" has an honest answer.
type Class string

const (
	// ClassPerson is traffic with no bot or infrastructure signature.
	ClassPerson Class = "person"
	// ClassBot is crawlers, AI scrapers, SEO tools, security scanners and raw
	// HTTP clients -- someone else's automation.
	ClassBot Class = "bot"
	// ClassInfra is our own automation: loopback health probes and the
	// third-party uptime checkers pointed at these sites.
	ClassInfra Class = "infra"
)

// infraUA matches monitoring that exists because we put it there. nginx-directory
// is the local probe on this box (127.0.0.1, every ~30s, every site); the rest are
// hosted uptime checkers. These are automation, but they are *our* automation, so
// they are kept separate from third-party crawlers rather than lumped in as bots.
var infraUA = []string{
	"nginx-directory",
	"uptimerobot",
	"pingdom",
	"statuscake",
	"betteruptime",
	"better uptime",
	"site24x7",
	"hetrixtools",
	"updown.io",
	"cron-job.org",
	"freshping",
	"uptime-kuma",
}

// botUA are substrings matched against a lowercased User-Agent. Grouped only for
// readability -- the matcher treats them as one flat list.
var botUA = []string{
	// Generic self-identification. "bot" alone catches the large majority of
	// well-behaved crawlers (Googlebot, bingbot, DuckDuckBot, ...).
	"bot", "crawl", "spider", "slurp", "scrape", "fetcher", "archiver",

	// HTTP clients and libraries -- never a person browsing.
	"curl/", "wget", "python-requests", "python-urllib", "urllib", "go-http-client",
	"java/", "okhttp", "libwww", "httpclient", "axios", "node-fetch", "got/",
	"guzzle", "postmanruntime", "insomnia", "aiohttp", "httpie", "restsharp",
	"typhoeus", "faraday", "lwp::simple", "winhttp", "powershell",

	// Headless / automated browsers.
	"headlesschrome", "phantomjs", "puppeteer", "playwright", "selenium",
	"chrome-lighthouse", "pagespeed", "gtmetrix",

	// AI crawlers and answer engines.
	"gptbot", "oai-searchbot", "chatgpt-user", "claudebot", "claude-web",
	"anthropic-ai", "perplexitybot", "bytespider", "ccbot", "amazonbot",
	"applebot", "google-extended", "cohere-ai", "diffbot", "meta-externalagent",
	"youbot", "timpibot", "omgili", "imagesiftbot",

	// Link unfurlers and social previews.
	"facebookexternalhit", "twitterbot", "slackbot", "discordbot", "telegrambot",
	"linkedinbot", "whatsapp", "embedly", "skypeuripreview", "pinterest",
	"redditbot", "quora link preview", "vkshare", "tumblr",

	// SEO / marketing suites.
	"semrush", "ahrefs", "mj12bot", "dotbot", "petalbot", "dataforseo",
	"blexbot", "serpstat", "seokicks", "megaindex", "sistrix", "screaming frog",
	"barkrowler", "zoominfo", "linkdex",

	// Security scanners, internet-wide measurement, exploit probes.
	"l9scan", "leakix", "censys", "zgrab", "masscan", "nmap", "nuclei",
	"sqlmap", "nikto", "wpscan", "expanse", "paloaltonetworks",
	"internet-measurement", "shodan", "netsystemsresearch", "criminalip",
	"binaryedge", "internetmeasurement", "researchscan", "cyberscan",

	// Feed readers and misc aggregators.
	"feedly", "feedburner", "feedfetcher", "newsblur", "inoreader", "rssbot",
	"apache-httpclient", "dataprovider", "seznam", "yandex", "baidu",
}

// botPath are request paths nothing legitimate on a static host ever asks for.
// A hit here is automation regardless of how the User-Agent presents itself.
var botPath = []string{
	"/wp-admin", "/wp-login", "/wp-content", "/wp-includes", "/xmlrpc.php",
	"/.env", "/.git", "/.aws", "/.ssh", "/phpmyadmin", "/pma/", "/adminer",
	"/vendor/phpunit", "/cgi-bin/", "/autodiscover", "/owa/", "/boaform",
	"/solr/", "/actuator", "/config.json", "/credentials", "/shell", "/eval-stdin",
	"/hudson", "/jenkins", "/manager/html", "/struts", "/druid/", "/_ignition",
}

// Classify decides which bucket one request belongs in. remoteAddr is nginx's
// $remote_addr, ua its $http_user_agent, uri the requested path (query included
// is fine -- it is trimmed here).
//
// Order is deliberate: infrastructure wins over everything (a loopback probe is
// ours no matter what it claims to be), then bot signatures, then person as the
// default. Defaulting to person means an unrecognised crawler inflates the human
// count rather than silently deleting real traffic -- the safer direction to be
// wrong in, since an inflated bot column would hide people from the owner.
func Classify(remoteAddr, ua, uri string) Class {
	// Loopback means the request never left the box: it is our own probe.
	if isLoopback(remoteAddr) {
		return ClassInfra
	}

	lower := strings.ToLower(strings.TrimSpace(ua))

	for _, s := range infraUA {
		if strings.Contains(lower, s) {
			return ClassInfra
		}
	}

	// No User-Agent at all: no real browser omits it.
	if lower == "" || lower == "-" {
		return ClassBot
	}

	// A UA that is itself a URL is an exploit-scanner calling card -- this box
	// logs hits with a literal "http://agent-deploy.dev/wp-admin/install.php".
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return ClassBot
	}

	for _, s := range botUA {
		if strings.Contains(lower, s) {
			return ClassBot
		}
	}

	path := strings.ToLower(uri)
	if q := strings.IndexByte(path, '?'); q >= 0 {
		path = path[:q]
	}
	for _, p := range botPath {
		if strings.Contains(path, p) {
			return ClassBot
		}
	}

	return ClassPerson
}

// isLoopback reports whether remoteAddr is 127.0.0.0/8 or ::1. The address may
// carry a port (nginx does not add one to $remote_addr, but be tolerant).
func isLoopback(remoteAddr string) bool {
	addr := strings.TrimSpace(remoteAddr)
	if addr == "" {
		return false
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	addr = strings.Trim(addr, "[]")
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}
