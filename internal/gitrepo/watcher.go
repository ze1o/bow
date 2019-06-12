package gitrepo

import (
	"fmt"
	"github.com/alwinius/keel/internal/workgroup"
	"github.com/alwinius/keel/provider/helm"
	"github.com/sirupsen/logrus"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/http"
	apps_v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/helm/pkg/manifest"
	"path/filepath"
	"regexp"
	"time"
)

func WatchRepo(g *workgroup.Group, client *kubernetes.Clientset, log logrus.FieldLogger, rs ...cache.ResourceEventHandler) {

	// here we retrieve the deployments from git and store k8s objects in a cache, this doesnt have to run often

	watch(g, client.AppsV1().RESTClient(), log, "deployments", new(apps_v1.Deployment), rs...)
}

func watch(g *workgroup.Group, c cache.Getter, log logrus.FieldLogger, resource string, objType runtime.Object, rs ...cache.ResourceEventHandler) {

	// TODO: use parameters, use cache

	g.Add(func(stop <-chan struct{}) {
		log := log.WithField("resource", resource)
		log.Println("started")
		defer log.Println("stopped")
		for  {
				path, _ := filepath.Abs("/home/alwin/projects/keel-tmp")
				url := "https://iteragit.iteratec.de/bachelors-thesis-aeb/petclinic-deployment.git"
				chartFolder := "/helm/petclinic/"
				finalManifests := cloneOrUpdateGit(path,url , , chartFolder)

				var properResources []runtime.Object
				for _, m := range finalManifests {
					if gr, err := yamlToGenericResource(m.Content); err == nil {
						properResources = append(properResources, gr)
					} else {
						log.Error(err)
					}
				}

				for _, r := range properResources {
					for _, reh := range rs {
						reh.OnAdd(r)
					}
				}
				time.Sleep(time.Second * 5)

		}
	})
}

type buffer struct {
	ev chan interface{}
	logrus.StdLogger
	rh cache.ResourceEventHandler
}

type addEvent struct {
	obj interface{}
}

type updateEvent struct {
	oldObj, newObj interface{}
}

type deleteEvent struct {
	obj interface{}
}

// NewBuffer returns a ResourceEventHandler which buffers and serialises ResourceEventHandler events.
func NewBuffer(g *workgroup.Group, rh cache.ResourceEventHandler, log logrus.FieldLogger, size int) cache.ResourceEventHandler {
	buf := &buffer{
		ev:        make(chan interface{}, size),
		StdLogger: log.WithField("context", "buffer"),
		rh:        rh,
	}
	g.Add(buf.loop)
	return buf
}

func (b *buffer) loop(stop <-chan struct{}) {
	b.Println("started")
	defer b.Println("stopped")

	for {
		select {
		case ev := <-b.ev:
			switch ev := ev.(type) {
			case *addEvent:
				b.rh.OnAdd(ev.obj)
			case *updateEvent:
				b.rh.OnUpdate(ev.oldObj, ev.newObj)
			case *deleteEvent:
				b.rh.OnDelete(ev.obj)
			default:
				b.Printf("unhandled event type: %T: %v", ev, ev)
			}
		case <-stop:
			return
		}
	}
}

func (b *buffer) OnAdd(obj interface{}) {
	b.send(&addEvent{obj})
}

func (b *buffer) OnUpdate(oldObj, newObj interface{}) {
	b.send(&updateEvent{oldObj, newObj})
}

func (b *buffer) OnDelete(obj interface{}) {
	b.send(&deleteEvent{obj})
}

func (b *buffer) send(ev interface{}) {
	select {
	case b.ev <- ev:
		// all good
	default:
		b.Printf("event channel is full, len: %v, cap: %v", len(b.ev), cap(b.ev))
		b.ev <- ev
	}
}

func cloneOrUpdateGit(path string, url string, username string, password string, chartFolder string) []manifest.Manifest {
	var r *git.Repository
	var err error
	if r, err = git.PlainOpen(path); err != nil {
		r, err = git.PlainClone(path, false, &git.CloneOptions{
			Auth: &http.BasicAuth{
				Username: username,
				Password: password,
			},
			URL: url,
		})
	}
	ref, err := r.Head()
	commit, err := r.CommitObject(ref.Hash())
	fmt.Println("last commit", commit.Message)

	finalManifests, err := helm.ProcessTemplate(path + chartFolder)
	if err != nil {
		fmt.Println(err)
	}

	return finalManifests

}

func yamlToGenericResource(r string) (runtime.Object, error) {
	acceptedK8sTypes := regexp.MustCompile(`(Deployment|StatefulSet|Cronjob)`) // TODO: fill properly or remove
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, groupVersionKind, err := decode([]byte(r), nil, nil)
	if err != nil {
		return nil, err
	}
	if !acceptedK8sTypes.MatchString(groupVersionKind.Kind) {
		return nil, fmt.Errorf("skipping object with type: %s", groupVersionKind.Kind)
	} else {
		return obj, nil
	}


}