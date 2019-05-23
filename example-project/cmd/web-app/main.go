package main

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"html/template"
	"log"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"geeks-accelerator/oss/saas-starter-kit/example-project/cmd/web-app/handlers"
	"geeks-accelerator/oss/saas-starter-kit/example-project/internal/platform/deploy"
	"geeks-accelerator/oss/saas-starter-kit/example-project/internal/platform/flag"
	img_resize "geeks-accelerator/oss/saas-starter-kit/example-project/internal/platform/img-resize"
	itrace "geeks-accelerator/oss/saas-starter-kit/example-project/internal/platform/trace"
	"geeks-accelerator/oss/saas-starter-kit/example-project/internal/platform/web"
	template_renderer "geeks-accelerator/oss/saas-starter-kit/example-project/internal/platform/web/template-renderer"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/go-redis/redis"
	"github.com/kelseyhightower/envconfig"
	"github.com/lib/pq"
	"go.opencensus.io/trace"
	awstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"
	sqltrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	redistrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/go-redis/redis"
	sqlxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/jmoiron/sqlx"
)

// build is the git version of this program. It is set using build flags in the makefile.
var build = "develop"

func main() {

	// =========================================================================
	// Logging

	log := log.New(os.Stdout, "WEB_APP : ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)

	// =========================================================================
	// Configuration
	var cfg struct {
		Env  string `default:"dev" envconfig:"ENV"`
		HTTP struct {
			Host         string        `default:"0.0.0.0:3000" envconfig:"HTTP_HOST"`
			ReadTimeout  time.Duration `default:"10s" envconfig:"HTTP_READ_TIMEOUT"`
			WriteTimeout time.Duration `default:"10s" envconfig:"HTTP_WRITE_TIMEOUT"`
		}
		HTTPS struct {
			Host         string        `default:"" envconfig:"HTTPS_HOST"`
			ReadTimeout  time.Duration `default:"5s" envconfig:"HTTPS_READ_TIMEOUT"`
			WriteTimeout time.Duration `default:"5s" envconfig:"HTTPS_WRITE_TIMEOUT"`
		}
		App struct {
			Name        string `default:"web-app" envconfig:"APP_NAME"`
			BaseUrl     string `default:"" envconfig:"APP_BASE_URL"`
			TemplateDir string `default:"./templates" envconfig:"APP_TEMPLATE_DIR"`
			StaticDir   string `default:"./static" envconfig:"APP_STATIC_DIR"`
			StaticS3    struct {
				S3Enabled         bool   `envconfig:"APP_STATIC_S3_ENABLED"`
				S3Bucket          string `envconfig:"APP_STATIC_S3_BUCKET"`
				S3KeyPrefix       string `default:"public/web_app/static" envconfig:"APP_STATIC_S3_KEY_PREFIX"`
				CloudFrontEnabled bool   `envconfig:"APP_STATIC_S3_CLOUDFRONT_ENABLED"`
				ImgResizeEnabled  bool   `envconfig:"APP_STATIC_S3_IMG_RESIZE_ENABLED"`
			}
			DebugHost       string        `default:"0.0.0.0:4000" envconfig:"APP_DEBUG_HOST"`
			ShutdownTimeout time.Duration `default:"5s" envconfig:"APP_SHUTDOWN_TIMEOUT"`
		}
		Redis struct {
			DialTimeout     time.Duration `default:"5s" envconfig:"REDIS_DIAL_TIMEOUT"`
			Host            string        `default:":6379" envconfig:"REDIS_HOST"`
			DB              int           `default:"1" envconfig:"REDIS_DB"`
			MaxmemoryPolicy string        `envconfig:"REDIS_MAXMEMORY_POLICY"`
		}
		DB struct {
			Host        string        `default:"127.0.0.1:5433" envconfig:"DB_HOST"`
			User        string        `default:"postgres" envconfig:"DB_USER"`
			Pass        string        `default:"postgres" envconfig:"DB_PASS" json:"-"` // don't print
			Database        string        `default:"shared" envconfig:"DB_DATABASE"`
			Driver        string        `default:"postgres" envconfig:"DB_DRIVER"`
			Timezone        string        `default:"utc" envconfig:"DB_TIMEZONE"`
			DisableTLS        bool        `default:"false" envconfig:"DB_DISABLE_TLS"`
		}
		Trace struct {
			Host         string        `default:"http://tracer:3002/v1/publish" envconfig:"TRACE_HOST"`
			BatchSize    int           `default:"1000" envconfig:"TRACE_BATCH_SIZE"`
			SendInterval time.Duration `default:"15s" envconfig:"TRACE_SEND_INTERVAL"`
			SendTimeout  time.Duration `default:"500ms" envconfig:"TRACE_SEND_TIMEOUT"`
		}
		AwsAccount struct {
			AccessKeyID     string `envconfig:"AWS_ACCESS_KEY_ID"`
			SecretAccessKey string `envconfig:"AWS_SECRET_ACCESS_KEY" json:"-"` // don't print
			Region          string `default:"us-east-1" envconfig:"AWS_REGION"`

			// Get an AWS session from an implicit source if no explicit
			// configuration is provided. This is useful for taking advantage of
			// EC2/ECS instance roles.
			UseRole bool `envconfig:"AWS_USE_ROLE"`
		}
		Auth struct {
			AwsSecretID   string        `default:"auth-secret-key" envconfig:"AUTH_AWS_SECRET_ID"`
			KeyExpiration time.Duration `default:"3600s" envconfig:"AUTH_KEY_EXPIRATION"`
		}
		BuildInfo struct {
			CiCommitRefName     string `envconfig:"CI_COMMIT_REF_NAME"`
			CiCommitRefSlug     string `envconfig:"CI_COMMIT_REF_SLUG"`
			CiCommitSha         string `envconfig:"CI_COMMIT_SHA"`
			CiCommitTag         string `envconfig:"CI_COMMIT_TAG"`
			CiCommitTitle       string `envconfig:"CI_COMMIT_TITLE"`
			CiCommitDescription string `envconfig:"CI_COMMIT_DESCRIPTION"`
			CiJobId             string `envconfig:"CI_COMMIT_JOB_ID"`
			CiJobUrl            string `envconfig:"CI_COMMIT_JOB_URL"`
			CiPipelineId        string `envconfig:"CI_COMMIT_PIPELINE_ID"`
			CiPipelineUrl       string `envconfig:"CI_COMMIT_PIPELINE_URL"`
		}
		CMD string `envconfig:"CMD"`
	}

	// The prefix used for loading env variables.
	// ie: export WEB_APP_ENV=dev
	envKeyPrefix := "WEB_APP"

	// For additional details refer to https://github.com/kelseyhightower/envconfig
	if err := envconfig.Process(envKeyPrefix, &cfg); err != nil {
		log.Fatalf("main : Parsing Config : %v", err)
	}

	if err := flag.Process(&cfg); err != nil {
		if err != flag.ErrHelp {
			log.Fatalf("main : Parsing Command Line : %v", err)
		}
		return // We displayed help.
	}

	// =========================================================================
	// Config Validation & Defaults

	// If base URL is empty, set the default value from the HTTP Host
	if cfg.App.BaseUrl == "" {
		baseUrl := cfg.HTTP.Host
		if !strings.HasPrefix(baseUrl, "http") {
			if strings.HasPrefix(baseUrl, "0.0.0.0:") {
				pts := strings.Split(baseUrl, ":")
				pts[0] = "127.0.0.1"
				baseUrl = strings.Join(pts, ":")
			} else if strings.HasPrefix(baseUrl, ":") {
				baseUrl = "127.0.0.1" + baseUrl
			}
			baseUrl = "http://" + baseUrl
		}
		cfg.App.BaseUrl = baseUrl
	}

	// =========================================================================
	// Log App Info

	// Print the build version for our logs. Also expose it under /debug/vars.
	expvar.NewString("build").Set(build)
	log.Printf("main : Started : Application Initializing version %q", build)
	defer log.Println("main : Completed")

	// Print the config for our logs. It's important to any credentials in the config
	// that could expose a security risk are excluded from being json encoded by
	// applying the tag `json:"-"` to the struct var.
	{
		cfgJSON, err := json.MarshalIndent(cfg, "", "    ")
		if err != nil {
			log.Fatalf("main : Marshalling Config to JSON : %v", err)
		}
		log.Printf("main : Config : %v\n", string(cfgJSON))
	}

	// TODO: Validate what is being written to the logs. We don't
	// want to leak credentials or anything that can be a security risk.
	log.Printf("main : Config : %v\n", string(cfgJSON))

	// =========================================================================
	// Init AWS Session
	var awsSession *session.Session
	if cfg.AwsAccount.UseRole {
		// Get an AWS session from an implicit source if no explicit
		// configuration is provided. This is useful for taking advantage of
		// EC2/ECS instance roles.
		awsSession = session.Must(session.NewSession())
	} else {
		creds := credentials.NewStaticCredentials(cfg.AwsAccount.AccessKeyID, cfg.AwsAccount.SecretAccessKey, "")
		awsSession = session.New(&aws.Config{Region: aws.String(cfg.AwsAccount.Region), Credentials: creds})
	}
	awsSession = awstrace.WrapSession(awsSession)

	// =========================================================================
	// Start Redis
	// Ensure the eviction policy on the redis cluster is set correctly.
	// 		AWS Elastic cache redis clusters by default have the volatile-lru.
	// 			volatile-lru: evict keys by trying to remove the less recently used (LRU) keys first, but only among keys that have an expire set, in order to make space for the new data added.
	// 			allkeys-lru: evict keys by trying to remove the less recently used (LRU) keys first, in order to make space for the new data added.
	//		Recommended to have eviction policy set to allkeys-lru
	log.Println("main : Started : Initialize Redis")
	redisClient := redistrace.NewClient(&redis.Options{
		Addr:        cfg.Redis.Host,
		DB:          cfg.Redis.DB,
		DialTimeout: cfg.Redis.DialTimeout,
	})
	defer redisClient.Close()

	evictPolicyConfigKey := "maxmemory-policy"

	// if the maxmemory policy is set for redis, make sure its set on the cluster
	// default not set and will based on the redis config values defined on the server
	if cfg.Redis.MaxmemoryPolicy != "" {
		err := redisClient.ConfigSet(evictPolicyConfigKey, cfg.Redis.MaxmemoryPolicy).Err()
		if err != nil {
			log.Fatalf("main : redis : ConfigSet maxmemory-policy : %v", err)
		}
	} else {
		evictPolicy, err := redisClient.ConfigGet(evictPolicyConfigKey).Result()
		if err != nil {
			log.Fatalf("main : redis : ConfigGet maxmemory-policy : %v", err)
		}

		if evictPolicy[1] != "allkeys-lru" {
			log.Printf("main : redis : ConfigGet maxmemory-policy : recommended to be set to allkeys-lru to avoid OOM")
		}
	}

	// =========================================================================
	// Start Database
	var dbUrl url.URL
	{
		// Query parameters.
		var q url.Values

		// Handle SSL Mode
		if cfg.DB.DisableTLS {
			q.Set("sslmode", "disable")
		} else {
			q.Set("sslmode", "require")
		}

		q.Set("timezone", cfg.DB.Timezone)

		// Construct url.
		dbUrl = url.URL{
			Scheme:   cfg.DB.Driver,
			User:     url.UserPassword(cfg.DB.User, cfg.DB.Pass),
			Host:     cfg.DB.Host,
			Path:     cfg.DB.Database,
			RawQuery: q.Encode(),
		}
	}

	// Register informs the sqlxtrace package of the driver that we will be using in our program.
	// It uses a default service name, in the below case "postgres.db". To use a custom service
	// name use RegisterWithServiceName.
	sqltrace.Register(cfg.DB.Driver, &pq.Driver{}, sqltrace.WithServiceName("my-service"))
	masterDb, err := sqlxtrace.Open(cfg.DB.Driver, dbUrl.String())
	if err != nil {
		log.Fatalf("main : Register DB : %s : %v", cfg.DB.Driver, err)
	}
	defer masterDb.Close()

	// =========================================================================
	// Deploy
	switch cfg.CMD {
	case "sync-static":
		// sync static files to S3
		if cfg.App.StaticS3.S3Enabled || cfg.App.StaticS3.CloudFrontEnabled {
			err = deploy.SyncS3StaticFiles(awsSession, cfg.App.StaticS3.S3Bucket, cfg.App.StaticS3.S3KeyPrefix, cfg.App.StaticDir)
			if err != nil {
				log.Fatalf("main : deploy : %v", err)
			}
		}
		return
	}

	// =========================================================================
	// URL Formatter
	// s3UrlFormatter is a help function used by to convert an s3 key to
	// a publicly available image URL.
	var staticS3UrlFormatter func(string) string
	if cfg.App.StaticS3.S3Enabled || cfg.App.StaticS3.CloudFrontEnabled || cfg.App.StaticS3.ImgResizeEnabled {
		s3UrlFormatter, err := deploy.S3UrlFormatter(awsSession, cfg.App.StaticS3.S3Bucket, cfg.App.StaticS3.S3KeyPrefix, cfg.App.StaticS3.CloudFrontEnabled)
		if err != nil {
			log.Fatalf("main : S3UrlFormatter failed : %v", err)
		}

		staticS3UrlFormatter = func(p string) string {
			// When the path starts with a forward slash its referencing a local file,
			// make sure the static file prefix is included
			if strings.HasPrefix(p, "/") {
				p = filepath.Join(cfg.App.StaticS3.S3KeyPrefix, p)
			}
			return s3UrlFormatter(p)
		}
	}

	// staticUrlFormatter is a help function used by template functions defined below.
	// If the app has an S3 bucket defined for the static directory, all references in the app
	// templates should be updated to use a fully qualified URL for either the public file on S3
	// on from the cloudfront distribution.
	var staticUrlFormatter func(string) string
	if cfg.App.StaticS3.S3Enabled || cfg.App.StaticS3.CloudFrontEnabled {
		staticUrlFormatter = staticS3UrlFormatter
	} else {
		baseUrl, err := url.Parse(cfg.App.BaseUrl)
		if err != nil {
			log.Fatalf("main : url Parse(%s) : %v", cfg.App.BaseUrl, err)
		}

		staticUrlFormatter = func(p string) string {
			baseUrl.Path = p
			return baseUrl.String()
		}
	}

	// =========================================================================
	// Template Renderer
	// Implements interface web.Renderer to support alternative renderer

	// Append query string value to break browser cache used for services
	// that render responses for a browser with the following:
	// 	1. when env=dev, the current timestamp will be used to ensure every
	// 		request will skip browser cache.
	// 	2. all other envs, ie stage and prod. The commit hash will be used to
	// 		ensure that all cache will be reset with each new deployment.
	browserCacheBusterQueryString := func() string {
		var v string
		if cfg.Env == "dev" {
			// On dev always break cache.
			v = fmt.Sprintf("%d", time.Now().UTC().Unix())
		} else {
			// All other envs, use the current commit hash for the build
			v = cfg.BuildInfo.CiCommitSha
		}
		return v
	}

	// Helper method for appending the browser cache buster as a query string to
	// support breaking browser cache when necessary
	browserCacheBusterFunc := browserCacheBuster(browserCacheBusterQueryString)

	// Need defined functions below since they require config values, able to add additional functions
	// here to extend functionality.
	tmplFuncs := template.FuncMap{
		"BuildInfo": func(k string) string {
			r := reflect.ValueOf(cfg.BuildInfo)
			f := reflect.Indirect(r).FieldByName(k)
			return f.String()
		},
		"SiteBaseUrl": func(p string) string {
			u, err := url.Parse(cfg.HTTP.Host)
			if err != nil {
				return "?"
			}
			u.Path = p
			return u.String()
		},
		"AssetUrl": func(p string) string {
			var u string
			if staticUrlFormatter != nil {
				u = staticUrlFormatter(p)
			} else {
				if !strings.HasPrefix(p, "/") {
					p = "/" + p
				}
				u = p
			}

			u = browserCacheBusterFunc(u)

			return u
		},
		"SiteAssetUrl": func(p string) string {
			var u string
			if staticUrlFormatter != nil {
				u = staticUrlFormatter(filepath.Join(cfg.App.Name, p))
			} else {
				if !strings.HasPrefix(p, "/") {
					p = "/" + p
				}
				u = p
			}

			u = browserCacheBusterFunc(u)

			return u
		},
		"SiteS3Url": func(p string) string {
			var u string
			if staticUrlFormatter != nil {
				u = staticUrlFormatter(filepath.Join(cfg.App.Name, p))
			} else {
				u = p
			}
			return u
		},
		"S3Url": func(p string) string {
			var u string
			if staticUrlFormatter != nil {
				u = staticUrlFormatter(p)
			} else {
				u = p
			}
			return u
		},
	}

	// Image Formatter - additional functions exposed to templates for resizing images
	// to support response web applications.
	if cfg.App.StaticS3.ImgResizeEnabled {

		imgResizeS3KeyPrefix := filepath.Join(cfg.App.StaticS3.S3KeyPrefix, "images/responsive")

		tmplFuncs["S3ImgSrcLarge"] = func(ctx context.Context, p string) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, []int{320, 480, 800}, true)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgThumbSrcLarge"] = func(ctx context.Context, p string) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, []int{320, 480, 800}, false)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgSrcMedium"] = func(ctx context.Context, p string) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, []int{320, 640}, true)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgThumbSrcMedium"] = func(ctx context.Context, p string) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, []int{320, 640}, false)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgSrcSmall"] = func(ctx context.Context, p string) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, []int{320}, true)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgThumbSrcSmall"] = func(ctx context.Context, p string) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, []int{320}, false)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgSrc"] = func(ctx context.Context, p string, sizes []int) template.HTMLAttr {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgSrc(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, sizes, true)
			return template.HTMLAttr(res)
		}
		tmplFuncs["S3ImgUrl"] = func(ctx context.Context, p string, size int) string {
			u := staticUrlFormatter(p)
			res, _ := img_resize.S3ImgUrl(ctx, redisClient, staticS3UrlFormatter, awsSession, cfg.App.StaticS3.S3Bucket, imgResizeS3KeyPrefix, u, size)
			return res
		}
	}

	//
	t := template_renderer.NewTemplate(tmplFuncs)

	// global variables exposed for rendering of responses with templates
	gvd := map[string]interface{}{
		"_App": map[string]interface{}{
			"ENV":          cfg.Env,
			"BuildInfo":    cfg.BuildInfo,
			"BuildVersion": build,
		},
	}

	// Custom error handler to support rendering user friendly error page for improved web experience.
	eh := func(ctx context.Context, w http.ResponseWriter, r *http.Request, renderer web.Renderer, statusCode int, er error) error {
		data := map[string]interface{}{}

		return renderer.Render(ctx, w, r,
			"base.tmpl",  // base layout file to be used for rendering of errors
			"error.tmpl", // generic format for errors, could select based on status code
			web.MIMETextHTMLCharsetUTF8,
			http.StatusOK,
			data,
		)
	}

	// Enable template renderer to reload and parse template files when generating a response of dev
	// for a more developer friendly process. Any changes to the template files will be included
	// without requiring re-build/re-start of service.
	// This only supports files that already exist, if a new template file is added, then the
	// serivce needs to be restarted, but not rebuilt.
	enableHotReload := cfg.Env == "dev"

	// Template Renderer used to generate HTML response for web experience.
	renderer, err := template_renderer.NewTemplateRenderer(cfg.App.TemplateDir, enableHotReload, gvd, t, eh)
	if err != nil {
		log.Fatalf("main : Marshalling Config to JSON : %v", err)
	}

	// =========================================================================
	// Start Tracing Support

	logger := func(format string, v ...interface{}) {
		log.Printf(format, v...)
	}

	log.Printf("main : Tracing Started : %s", cfg.Trace.Host)
	exporter, err := itrace.NewExporter(logger, cfg.Trace.Host, cfg.Trace.BatchSize, cfg.Trace.SendInterval, cfg.Trace.SendTimeout)
	if err != nil {
		log.Fatalf("main : RegiTracingster : ERROR : %v", err)
	}
	defer func() {
		log.Printf("main : Tracing Stopping : %s", cfg.Trace.Host)
		batch, err := exporter.Close()
		if err != nil {
			log.Printf("main : Tracing Stopped : ERROR : Batch[%d] : %v", batch, err)
		} else {
			log.Printf("main : Tracing Stopped : Flushed Batch[%d]", batch)
		}
	}()

	trace.RegisterExporter(exporter)
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})

	// =========================================================================
	// Start Debug Service. Not concerned with shutting this down when the
	// application is being shutdown.
	//
	// /debug/vars - Added to the default mux by the expvars package.
	// /debug/pprof - Added to the default mux by the net/http/pprof package.
	if cfg.App.DebugHost != "" {
		go func() {
			log.Printf("main : Debug Listening %s", cfg.App.DebugHost)
			log.Printf("main : Debug Listener closed : %v", http.ListenAndServe(cfg.App.DebugHost, http.DefaultServeMux))
		}()
	}

	// =========================================================================
	// Start APP Service

	// Make a channel to listen for an interrupt or terminate signal from the OS.
	// Use a buffered channel because the signal package requires it.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	api := http.Server{
		Addr:           cfg.HTTP.Host,
		Handler:        handlers.APP(shutdown, log, cfg.App.StaticDir, cfg.App.TemplateDir, masterDb, nil, renderer),
		ReadTimeout:    cfg.HTTP.ReadTimeout,
		WriteTimeout:   cfg.HTTP.WriteTimeout,
		MaxHeaderBytes: 1 << 20,
	}

	// Make a channel to listen for errors coming from the listener. Use a
	// buffered channel so the goroutine can exit if we don't collect this error.
	serverErrors := make(chan error, 1)

	// Start the service listening for requests.
	go func() {
		log.Printf("main : APP Listening %s", cfg.HTTP.Host)
		serverErrors <- api.ListenAndServe()
	}()

	// =========================================================================
	// Shutdown

	// Blocking main and waiting for shutdown.
	select {
	case err := <-serverErrors:
		log.Fatalf("main : Error starting server: %v", err)

	case sig := <-shutdown:
		log.Printf("main : %v : Start shutdown..", sig)

		// Create context for Shutdown call.
		ctx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
		defer cancel()

		// Asking listener to shutdown and load shed.
		err := api.Shutdown(ctx)
		if err != nil {
			log.Printf("main : Graceful shutdown did not complete in %v : %v", cfg.App.ShutdownTimeout, err)
			err = api.Close()
		}

		// Log the status of this shutdown.
		switch {
		case sig == syscall.SIGSTOP:
			log.Fatal("main : Integrity issue caused shutdown")
		case err != nil:
			log.Fatalf("main : Could not stop server gracefully : %v", err)
		}
	}
}

// browserCacheBuster appends a the query string param v to a given url with
// a value based on the value returned from cacheBusterValueFunc
func browserCacheBuster(cacheBusterValueFunc func() string) func(uri string) string {
	f := func(uri string) string {
		v := cacheBusterValueFunc()
		if v == "" {
			return uri
		}

		u, err := url.Parse(uri)
		if err != nil {
			return ""
		}
		q := u.Query()
		q.Set("v", v)
		u.RawQuery = q.Encode()

		return u.String()
	}

	return f
}
