package cmd

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/divyam234/teldrive/api"
	"github.com/divyam234/teldrive/internal/config"
	"github.com/divyam234/teldrive/internal/database"
	"github.com/divyam234/teldrive/internal/duration"
	"github.com/divyam234/teldrive/internal/kv"
	"github.com/divyam234/teldrive/internal/logging"
	"github.com/divyam234/teldrive/internal/middleware"
	"github.com/divyam234/teldrive/internal/tgc"
	"github.com/divyam234/teldrive/internal/utils"
	"github.com/divyam234/teldrive/pkg/controller"
	"github.com/divyam234/teldrive/pkg/cron"
	"github.com/divyam234/teldrive/pkg/services"
	"github.com/gin-contrib/gzip"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/fx"
	"go.uber.org/zap/zapcore"
)

func NewRun() *cobra.Command {
	config := config.Config{}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start Teldrive Server",
		Run: func(cmd *cobra.Command, args []string) {
			runApplication(&config)

		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initViperConfig(cmd)
		},
	}

	runCmd.Flags().StringP("config", "c", "", "config file (default is $HOME/.teldrive/config.toml)")
	runCmd.Flags().IntVarP(&config.Server.Port, "server-port", "p", 8080, "Server port")
	duration.DurationVar(runCmd.Flags(), &config.Server.GracefulShutdown, "server-graceful-shutdown", 15*time.Second, "Server graceful shutdown timeout")

	runCmd.Flags().IntVarP(&config.Log.Level, "log-level", "", -1, "Logging level")
	runCmd.Flags().StringVar(&config.Log.File, "log-file", "", "Logging file path")
	runCmd.Flags().BoolVar(&config.Log.Development, "log-development", false, "Enable development mode")

	runCmd.Flags().StringVar(&config.JWT.Secret, "jwt-secret", "", "JWT secret key")
	duration.DurationVar(runCmd.Flags(), &config.JWT.SessionTime, "jwt-session-time", (30*24)*time.Hour, "JWT session duration")
	runCmd.Flags().StringSliceVar(&config.JWT.AllowedUsers, "jwt-allowed-users", []string{}, "Allowed users")

	runCmd.Flags().StringVar(&config.DB.DataSource, "db-data-source", "", "Database connection string")
	runCmd.Flags().IntVar(&config.DB.LogLevel, "db-log-level", 1, "Database log level")
	runCmd.Flags().BoolVar(&config.DB.PrepareStmt, "db-prepare-stmt", true, "Enable prepared statements")
	runCmd.Flags().BoolVar(&config.DB.Pool.Enable, "db-pool-enable", true, "Enable database pool")
	runCmd.Flags().IntVar(&config.DB.Pool.MaxIdleConnections, "db-pool-max-open-connections", 25, "Database max open connections")
	runCmd.Flags().IntVar(&config.DB.Pool.MaxIdleConnections, "db-pool-max-idle-connections", 25, "Database max idle connections")
	duration.DurationVar(runCmd.Flags(), &config.DB.Pool.MaxLifetime, "db-pool-max-lifetime", 10*time.Minute, "Database max connection lifetime")

	runCmd.Flags().IntVar(&config.TG.AppId, "tg-app-id", 0, "Telegram app ID")
	runCmd.Flags().StringVar(&config.TG.AppHash, "tg-app-hash", "", "Telegram app hash")
	runCmd.Flags().StringVar(&config.TG.SessionFile, "tg-session-file", "", "Bot session file path")
	runCmd.Flags().BoolVar(&config.TG.RateLimit, "tg-rate-limit", true, "Enable rate limiting")
	runCmd.Flags().IntVar(&config.TG.RateBurst, "tg-rate-burst", 5, "Limiting burst")
	runCmd.Flags().IntVar(&config.TG.Rate, "tg-rate", 100, "Limiting rate")
	runCmd.Flags().StringVar(&config.TG.DeviceModel, "tg-device-model",
		"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/116.0", "Device model")
	runCmd.Flags().StringVar(&config.TG.SystemVersion, "tg-system-version", "Win32", "System version")
	runCmd.Flags().StringVar(&config.TG.AppVersion, "tg-app-version", "4.6.3 K", "App version")
	runCmd.Flags().StringVar(&config.TG.LangCode, "tg-lang-code", "en", "Language code")
	runCmd.Flags().StringVar(&config.TG.SystemLangCode, "tg-system-lang-code", "en-US", "System language code")
	runCmd.Flags().StringVar(&config.TG.LangPack, "tg-lang-pack", "webk", "Language pack")
	runCmd.Flags().StringVar(&config.TG.Proxy, "tg-proxy", "", "HTTP OR SOCKS5 proxy URL")
	runCmd.Flags().IntVar(&config.TG.BgBotsLimit, "tg-bg-bots-limit", 5, "Background bots limit")
	runCmd.Flags().BoolVar(&config.TG.DisableStreamBots, "tg-disable-stream-bots", false, "Disable stream bots")
	runCmd.Flags().BoolVar(&config.TG.EnableLogging, "tg-enable-logging", false, "Enable telegram client logging")
	runCmd.Flags().StringVar(&config.TG.Uploads.EncryptionKey, "tg-uploads-encryption-key", "", "Uploads encryption key")
	runCmd.Flags().IntVar(&config.TG.Uploads.Threads, "tg-uploads-threads", 8, "Uploads threads")
	runCmd.Flags().IntVar(&config.TG.Uploads.MaxRetries, "tg-uploads-max-retries", 10, "Uploads Retries")
	runCmd.Flags().Int64Var(&config.TG.PoolSize, "tg-pool-size", 8, "Telegram Session pool size")
	duration.DurationVar(runCmd.Flags(), &config.TG.ReconnectTimeout, "tg-reconnect-timeout", 5*time.Minute, "Reconnect Timeout")
	duration.DurationVar(runCmd.Flags(), &config.TG.Uploads.Retention, "tg-uploads-retention", (24*7)*time.Hour, "Uploads retention duration")
	runCmd.Flags().IntVar(&config.TG.Stream.MultiThreads, "tg-stream-multi-threads", 0, "Stream multi-threads")
	runCmd.Flags().IntVar(&config.TG.Stream.Buffers, "tg-stream-buffers", 8, "No of Stream buffers")
	duration.DurationVar(runCmd.Flags(), &config.TG.Stream.ChunkTimeout, "tg-stream-chunk-timeout", 30*time.Second, "Chunk Fetch Timeout")
	runCmd.MarkFlagRequired("tg-app-id")
	runCmd.MarkFlagRequired("tg-app-hash")
	runCmd.MarkFlagRequired("db-data-source")
	runCmd.MarkFlagRequired("jwt-secret")

	return runCmd
}

func runApplication(conf *config.Config) {
	logging.SetConfig(&logging.Config{
		Level:       zapcore.Level(conf.Log.Level),
		Development: conf.Log.Development,
		FilePath:    conf.Log.File,
	})

	tgContext, cancel := context.WithCancel(context.Background())

	defer func() {
		logging.DefaultLogger().Sync()
		cancel()
	}()

	app := fx.New(
		fx.Supply(conf),
		fx.Supply(logging.DefaultLogger().Desugar()),
		fx.NopLogger,
		fx.StopTimeout(conf.Server.GracefulShutdown+time.Second),
		fx.Invoke(
			initApp,
			cron.StartCronJobs,
		),
		fx.Provide(
			database.NewDatabase,
			kv.NewBoltKV,
			tgc.NewStreamWorker(tgContext),
			tgc.NewUploadWorker,
			services.NewAuthService,
			services.NewFileService,
			services.NewUploadService,
			services.NewUserService,
			controller.NewController,
		),
	)

	app.Run()
}

func initViperConfig(cmd *cobra.Command) error {

	viper.SetConfigType("toml")

	cfgFile := cmd.Flags().Lookup("config").Value.String()

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, _ := homedir.Dir()
		viper.AddConfigPath(filepath.Join(home, ".teldrive"))
		viper.AddConfigPath(".")
		viper.AddConfigPath(utils.ExecutableDir())
		viper.SetConfigName("config")
	}

	viper.SetEnvPrefix("teldrive")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()
	viper.ReadInConfig()
	bindFlags(cmd.Flags(), "", reflect.ValueOf(config.Config{}))
	return nil

}
func bindFlags(flags *pflag.FlagSet, prefix string, v reflect.Value) {
	t := v.Type()
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	for i := range t.NumField() {
		field := t.Field(i)
		switch field.Type.Kind() {
		case reflect.Struct:
			bindFlags(flags, fmt.Sprintf("%s.%s", prefix, strings.ToLower(field.Name)), v.Field(i))
		default:
			newPrefix := prefix[1:]
			newName := modifyFlag(field.Name)
			configName := fmt.Sprintf("%s.%s", newPrefix, newName)
			flag := flags.Lookup(fmt.Sprintf("%s-%s", strings.ReplaceAll(newPrefix, ".", "-"), newName))
			if !flag.Changed && viper.IsSet(configName) {
				confVal := viper.Get(configName)
				if field.Type.Kind() == reflect.Slice {
					sliceValue, ok := confVal.([]interface{})
					if ok {
						for _, v := range sliceValue {
							flag.Value.Set(fmt.Sprintf("%v", v))
						}
					}
				} else {
					flags.Set(flag.Name, fmt.Sprintf("%v", confVal))
				}
			}
		}
	}
}

func modifyFlag(s string) string {
	var result []rune

	for i, c := range s {
		if i > 0 && unicode.IsUpper(c) {
			result = append(result, '-')
		}
		result = append(result, unicode.ToLower(c))
	}

	return string(result)
}

func initApp(lc fx.Lifecycle, cfg *config.Config, c *controller.Controller) *gin.Engine {

	gin.SetMode(gin.ReleaseMode)

	r := gin.New()

	r.Use(gin.Recovery())

	r.Use(ginzap.GinzapWithConfig(logging.DefaultLogger().Desugar(), &ginzap.Config{
		TimeFormat: time.RFC3339,
		UTC:        true,
		SkipPaths:  []string{"/favicon.ico", "/assets"},
	}))

	r.Use(middleware.Cors())

	r.Use(func(c *gin.Context) {
		pattern := `/(assets|images|fonts)/.*\.(js|css|svg|jpeg|jpg|png|woff|woff2|ttf|json|webp|png|ico|txt)$`
		re, _ := regexp.Compile(pattern)
		if re.MatchString(c.Request.URL.Path) {
			c.Writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			gzip.Gzip(gzip.DefaultCompression)(c)
		}
		c.Next()
	})

	r = api.InitRouter(r, c, cfg)
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: r,
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logging.FromContext(ctx).Infof("Started server http://localhost:%d", cfg.Server.Port)

			go func() {

				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logging.DefaultLogger().Errorw("failed to close http server", "err", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logging.FromContext(ctx).Info("Stopped server")
			return srv.Shutdown(ctx)
		},
	})
	return r
}
