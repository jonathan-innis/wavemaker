package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/Pallinder/go-randomdata"
	"github.com/avast/retry-go"
	"github.com/samber/lo"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	discoveryLabel = "wavemaker.io/wave"
	pauseContainer = "public.ecr.aws/eks-distro/kubernetes/pause:3.2"
)

func waitRetryOptions(ctx context.Context) []retry.Option {
	return []retry.Option{
		retry.Context(ctx),
		retry.Delay(5 * time.Second),
		retry.DelayType(retry.FixedDelay),
		retry.LastErrorOnly(true),
		retry.Attempts(60),
	}
}

var intervalStr, durationStr, resourceRequestsStr string
var count int

func init() {
	flag.StringVar(&intervalStr, "interval", "1m", "Interval in a golang duration value between scale-ups")
	flag.StringVar(&durationStr, "duration", "1m", "Duration in a golang duration to maintain the scale-up")
	flag.IntVar(&count, "count", 100, "Count of pods to scale-up")
	flag.StringVar(&resourceRequestsStr, "requests", "cpu=100m,memory=100Mi", "Resource requests for the pods that are scaled-up")

	flag.Parse()
}

func main() {
	ctx := context.Background()
	logger := lo.Must(zap.NewProduction()).Sugar()
	config := controllerruntime.GetConfigOrDie()
	cache := lo.Must(cache.New(config, cache.Options{}))
	kubeClient := lo.Must(client.New(config, client.Options{Cache: &client.CacheOptions{Reader: cache}}))

	interval := lo.Must(time.ParseDuration(intervalStr))
	duration := lo.Must(time.ParseDuration(durationStr))
	resourceRequests := parseResourceRequestsString(resourceRequestsStr)

	go func() {
		lo.Must0(cache.Start(ctx))
	}()
	for {
		createPods(ctx, logger, kubeClient, count, resourceRequests)
		waitForReady(ctx, logger, kubeClient)

		logger.Info("running wave")
		// Wait until the duration timeout expires
		select {
		case <-time.After(duration):
		case <-ctx.Done():
			return
		}

		deprovisionPods(ctx, logger, kubeClient)
		waitForTerminated(ctx, logger, kubeClient)

		logger.Info("completed wave")
		// Wait until the interval timeout expires to start another loop
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return
		}
	}
}

func createPods(ctx context.Context, logger *zap.SugaredLogger, kubeClient client.Client, count int, resourceRequests v1.ResourceList) {
	successCount := 0
	prefix := strings.ToLower(randomdata.SillyName())
	for i := 0; i < count; i++ {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: fmt.Sprintf("%s-", prefix),
				Namespace:    "default",
				Labels: map[string]string{
					discoveryLabel: "true",
				},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "default",
						Image: pauseContainer,
						Resources: v1.ResourceRequirements{
							Requests: resourceRequests,
						},
					},
				},
				Affinity: &v1.Affinity{
					PodAntiAffinity: &v1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										discoveryLabel: "true",
									},
								},
								TopologyKey: v1.LabelHostname,
							},
						},
					},
				},
			},
		}
		if err := kubeClient.Create(ctx, pod); err != nil {
			logger.With(pod, pod.Name).Errorf("creating pod, %v", err)
		} else {
			successCount++
		}
	}
	logger.With("pods", successCount).Info("created pods")
}

func waitForReady(ctx context.Context, logger *zap.SugaredLogger, kubeClient client.Client) {
	_ = retry.Do(func() error {
		pods := &v1.PodList{}
		if err := kubeClient.List(ctx, pods, client.HasLabels{discoveryLabel}); err != nil {
			logger.Errorf("listing pods, %v", err)
			return err
		}
		pods.Items = lo.Reject(pods.Items, func(p v1.Pod, _ int) bool {
			cond, ok := lo.Find(p.Status.Conditions, func(c v1.PodCondition) bool {
				return c.Type == v1.PodReady
			})
			return ok && cond.Status == v1.ConditionTrue
		})
		if len(pods.Items) > 0 {
			logger.With("remaining", len(pods.Items)).Info("waiting on remaining pods to go ready")
			return fmt.Errorf("not all pods are ready, %d remaining", len(pods.Items))
		}
		logger.Info("all pods are ready")
		return nil
	}, waitRetryOptions(ctx)...)
}

func deprovisionPods(ctx context.Context, logger *zap.SugaredLogger, kubeClient client.Client) {
	successCount := 0
	pods := &v1.PodList{}
	if err := kubeClient.List(ctx, pods, client.HasLabels{discoveryLabel}); err != nil {
		logger.Errorf("listing pods, %v", err)
		return
	}
	for i := range pods.Items {
		if err := kubeClient.Delete(ctx, &pods.Items[i]); client.IgnoreNotFound(err) != nil {
			logger.With("pod", pods.Items[i].Name).Errorf("deleting pod, %v", err)
		} else {
			successCount++
		}
	}
	logger.With("pods", successCount).Info("all pods are terminated")
}

func waitForTerminated(ctx context.Context, logger *zap.SugaredLogger, kubeClient client.Client) {
	_ = retry.Do(func() error {
		pods := &v1.PodList{}
		if err := kubeClient.List(ctx, pods, client.HasLabels{discoveryLabel}); err != nil {
			logger.Errorf("listing pods, %v", err)
			return err
		}
		if len(pods.Items) > 0 {
			logger.With("remaining", len(pods.Items)).Info("waiting on remaining pods to terminate")
			return fmt.Errorf("not all pods are terminated, %d remaining", len(pods.Items))
		}
		return nil
	}, waitRetryOptions(ctx)...)
}

func parseResourceRequestsString(s string) v1.ResourceList {
	l := strings.Split(s, ",")
	return lo.SliceToMap(l, func(s string) (v1.ResourceName, resource.Quantity) {
		vals := strings.Split(strings.Trim(s, " "), "=")
		return v1.ResourceName(vals[0]), resource.MustParse(vals[1])
	})
}
