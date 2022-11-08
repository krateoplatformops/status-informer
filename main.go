package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/krateoplatformops/status-informer/internal/informer"
	"github.com/krateoplatformops/status-informer/internal/support"
	"github.com/rs/zerolog"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	serviceName = "StatusInformer"
)

var (
	Build string
)

func main() {
	// Flags
	kubeconfig := flag.String(clientcmd.RecommendedConfigPathFlag, "", "absolute path to the kubeconfig file")
	debug := flag.Bool("debug",
		support.EnvBool("STATUS_INFORMER_DEBUG", false), "dump verbose output")
	resyncInterval := flag.Duration("resync-interval",
		support.EnvDuration("STATUS_INFORMER_RESYNC_INTERVAL", time.Minute*1), "resync interval")
	resourceGroup := flag.String("group",
		support.EnvString("STATUS_INFORMER_GROUP", "cluster.x-k8s.io"), "resource api group")
	resourceVersion := flag.String("version",
		support.EnvString("STATUS_INFORMER_VERSION", "v1beta1"), "resource api version")
	resourceName := flag.String("resource",
		support.EnvString("STATUS_INFORMER_RESOURCE", "clusters"), "resource name")

	throttlePeriod := flag.Duration("throttle-period",
		support.EnvDuration("STATUS_INFORMER_THROTTLE_PERIOD", 0), "throttle period")

	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Flags:")
		flag.PrintDefaults()
	}

	flag.Parse()

	// Initialize the logger
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Default level for this log is info, unless debug flag is present
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log := zerolog.New(os.Stdout).With().
		Str("service", serviceName).
		Timestamp().
		Logger()

	support.FixKubernetesServicePortEventually()

	// Kubernetes configuration
	var cfg *rest.Config
	var err error
	if len(*kubeconfig) > 0 {
		cfg, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatal().Err(err).Msg("Building kubeconfig.")
	}

	worker, err := informer.NewStatusInformer(informer.StatusInformerOpts{
		RESTConfig:     cfg,
		Log:            log,
		ResyncInterval: *resyncInterval,
		ThrottlePeriod: *throttlePeriod,
		GroupVersionResource: schema.GroupVersionResource{
			Group:    *resourceGroup,
			Version:  *resourceVersion,
			Resource: *resourceName,
		},
	})
	if err != nil {
		log.Fatal().Err(err).Msg("Creating the resource status informer.")
	}

	stop := sigHandler(log)

	// Startup the StatusInformer
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		log.Info().
			Str("build", Build).
			Bool("debug", *debug).
			Dur("resyncInterval", *resyncInterval).
			Dur("throttlePeriod", *throttlePeriod).
			Str("group", *resourceGroup).
			Str("version", *resourceVersion).
			Str("resource", *resourceName).
			Msgf("Starting %s.", serviceName)

		worker.Run(stop)
	}()

	wg.Wait()
	log.Warn().Msgf("%s done.", serviceName)
	os.Exit(1)
}

// setup a signal hander to gracefully exit
func sigHandler(log zerolog.Logger) <-chan struct{} {
	stop := make(chan struct{})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c,
			syscall.SIGINT,  // Ctrl+C
			syscall.SIGTERM, // Termination Request
			syscall.SIGSEGV, // FullDerp
			syscall.SIGABRT, // Abnormal termination
			syscall.SIGILL,  // illegal instruction
			syscall.SIGFPE)  // floating point - this is why we can't have nice things
		sig := <-c
		log.Warn().Msgf("Signal (%v) detected, shutting down", sig)
		close(stop)
	}()
	return stop
}
