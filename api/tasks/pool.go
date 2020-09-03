package tasks

import (
	"strconv"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/ansible-semaphore/semaphore/util"
)

type taskPool struct {
	queue       []*task
	register    chan *task
	activeProj  map[int]*task
	activeNodes map[string]*task
	running     int
}

var pool = taskPool{
	queue:       make([]*task, 0),
	register:    make(chan *task),
	activeProj:  make(map[int]*task),
	activeNodes: make(map[string]*task),
	running:     0,
}

type resourceLock struct {
	lock   bool
	holder *task
}

var resourceLocker = make(chan *resourceLock)

//nolint: gocyclo
func (p *taskPool) run() {
	ticker := time.NewTicker(5 * time.Second)

	defer func() {
		close(resourceLocker)
		ticker.Stop()
	}()

	// Lock or unlock resources when running a task
	go func(locker <-chan *resourceLock) {
		for l := range locker {
			t := l.holder

			if l.lock {
				if p.blocks(t) {
					panic("Trying to lock an already locked resource!")
				}

				p.activeProj[t.projectID] = t

				for _, node := range t.hosts {
					p.activeNodes[node] = t
				}

				p.running++
				continue
			}

			if p.activeProj[t.projectID] == t {
				delete(p.activeProj, t.projectID)
			}

			for _, node := range t.hosts {
				delete(p.activeNodes, node)
			}

			p.running--
		}
	}(resourceLocker)

	for {
		select {
		case task := <-p.register:
			p.addToPriorityQueue(task)
			log.Debug(task)
			msg := "Task " + strconv.Itoa(task.task.ID) + " added to queue"
			task.log(msg)
			log.Info(msg)
		case <-ticker.C:
			if len(p.queue) == 0 {
				continue
			}

			//get task from top of queue
			t := p.queue[0]
			if t.task.Status == taskFailStatus {
				//delete failed task from queue
				p.eraseFirstTask()
				log.Info("Task " + strconv.Itoa(t.task.ID) + " removed from queue")
				continue
			}
			if p.blocks(t) {
				//move blocked task to end of queue by increasing its priority
				t.priority = 100
				p.minHeapify(0)
				continue
			}
			log.Info("Set resourse locker with task " + strconv.Itoa(t.task.ID))
			resourceLocker <- &resourceLock{lock: true, holder: t}
			if !t.prepared {
				t.priority = -1
				go t.prepareRun()
				continue
			}
			go t.run()
			p.eraseFirstTask()
			log.Info("Task " + strconv.Itoa(t.task.ID) + " removed from queue")
		}
	}
}

func (p *taskPool) blocks(t *task) bool {
	if p.running >= util.Config.MaxParallelTasks {
		return true
	}

	switch util.Config.ConcurrencyMode {
	case "project":
		return p.activeProj[t.projectID] != nil
	case "node":
		for _, node := range t.hosts {
			if p.activeNodes[node] != nil {
				return true
			}
		}

		return false
	default:
		return p.running > 0
	}
}

func (p *taskPool) addToPriorityQueue(t *task) {
	p.queue = append(p.queue, t)
	id := len(p.queue) - 1
	for id != 0 && p.queue[(id-1)/2].priority > p.queue[id].priority {
		tempTask := p.queue[id]
		p.queue[id] = p.queue[(id-1)/2]
		p.queue[(id-1)/2] = tempTask
		id = (id - 1) / 2
	}
}

func (p *taskPool) minHeapify(id int) {

	var minChldID int

	if 2*id+2 < len(p.queue) {
		if p.queue[2*id+1].priority < p.queue[2*id+2].priority {
			minChldID = 2*id + 1
		} else {
			minChldID = 2*id + 2
		}
	} else if 2*id+1 < len(p.queue) {
		minChldID = 2*id + 1
	} else {
		return
	}

	if p.queue[minChldID].priority < p.queue[id].priority {
		tempTask := p.queue[id]
		p.queue[id] = p.queue[minChldID]
		p.queue[minChldID] = tempTask
		p.minHeapify(minChldID)
	}

}

func (p *taskPool) eraseFirstTask() {
	p.queue[0] = p.queue[len(p.queue)-1]
	p.queue = p.queue[:len(p.queue)-1]
	if len(p.queue) != 0 {
		p.minHeapify(0)
	}
}

// StartRunner begins the task pool, used as a goroutine
func StartRunner() {
	pool.run()
}
