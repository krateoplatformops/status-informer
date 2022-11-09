package informer

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/krateoplatformops/status-informer/internal/shortid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
)

type StatusInformer struct {
	log            zerolog.Logger
	dynamicClient  dynamic.Interface
	informer       cache.SharedInformer
	throttlePeriod time.Duration
	sid            *shortid.Shortid
	recorder       record.EventRecorder
}

type StatusInformerOpts struct {
	RESTConfig           *rest.Config
	ResyncInterval       time.Duration
	ThrottlePeriod       time.Duration
	Log                  zerolog.Logger
	GroupVersionResource schema.GroupVersionResource
	Recorder             record.EventRecorder
}

// NewStatusInformer will create a new status watcher using the input params
func NewStatusInformer(opts StatusInformerOpts) (*StatusInformer, error) {
	// Grab a dynamic interface that we can create informers from
	dc, err := dynamic.NewForConfig(opts.RESTConfig)
	if err != nil {
		return nil, err
	}
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		dc, opts.ResyncInterval, corev1.NamespaceAll, nil)

	informer := factory.ForResource(opts.GroupVersionResource)
	if informer == nil {
		return nil, fmt.Errorf("generic informer for resource '%s' not found", opts.GroupVersionResource)
	}

	return &StatusInformer{
		informer:       informer.Informer(),
		log:            opts.Log,
		throttlePeriod: opts.ThrottlePeriod,
		dynamicClient:  dc,
		recorder:       opts.Recorder,
	}, nil
}

// Run starts the Watcher.
func (w *StatusInformer) Run(stopCh <-chan struct{}) {
	w.informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				w.onObject(obj)
			},
			UpdateFunc: func(_, newObj interface{}) {
				w.onObject(newObj)
			},
			DeleteFunc: func(obj interface{}) {
				w.onObject(obj)
			},
		},
	)

	defer utilruntime.HandleCrash()

	w.informer.Run(stopCh)
	//w.factory.Start(stopCh)

	// here is where we kick the caches into gear
	if !cache.WaitForCacheSync(stopCh, w.informer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}

	<-stopCh
}

func (w *StatusInformer) onObject(obj interface{}) {
	unstr, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}

	status := w.findStatus(unstr)
	if status == nil || len(status.Conditions) == 0 {
		return
	}

	for _, cond := range status.Conditions {
		ref := corev1.ObjectReference{
			UID:             unstr.GetUID(),
			Kind:            unstr.GetKind(),
			Name:            unstr.GetName(),
			Namespace:       unstr.GetNamespace(),
			APIVersion:      unstr.GetAPIVersion(),
			ResourceVersion: unstr.GetResourceVersion(),
		}

		eventType := corev1.EventTypeNormal
		if cond.Status != ConditionStatus(corev1.ConditionTrue) {
			eventType = corev1.EventTypeWarning
		}

		w.recorder.Event(&ref, eventType, cond.Reason, cond.Message)
	}
}

func (w *StatusInformer) findStatus(unstr *unstructured.Unstructured) *Status {
	res, ok, err := unstructured.NestedMap(unstr.UnstructuredContent(), "status")
	if err != nil {
		w.log.Error().Err(err).
			Str("apiVersion", unstr.GetAPIVersion()).
			Str("kind", unstr.GetKind()).
			Str("name", unstr.GetName()).
			Str("namespace", unstr.GetNamespace()).
			Msg("Looking for status.")
		return nil
	}
	if !ok {
		return nil
	}

	status := &Status{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructuredWithValidation(res, status, false)
	if err != nil {
		w.log.Error().Err(err).
			Str("apiVersion", unstr.GetAPIVersion()).
			Str("kind", unstr.GetKind()).
			Str("name", unstr.GetName()).
			Str("namespace", unstr.GetNamespace()).
			Msg("Deserializing status.")
		return nil
	}

	return status
}
