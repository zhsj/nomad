package scheduler

import (
	"math"
	"time"

	"github.com/hashicorp/nomad/nomad/structs"
)

const (
	// serviceJobAntiAffinityPenalty is the penalty applied
	// to the score for placing an alloc on a node that
	// already has an alloc for this job.
	serviceJobAntiAffinityPenalty = 10.0

	// batchJobAntiAffinityPenalty is the same as the
	// serviceJobAntiAffinityPenalty but for batch type jobs.
	batchJobAntiAffinityPenalty = 5.0
)

// Stack is a chained collection of iterators. The stack is used to
// make placement decisions. Different schedulers may customize the
// stack they use to vary the way placements are made.
type Stack interface {
	// SetNodes is used to set the base set of potential nodes
	SetNodes([]*structs.Node)

	// SetTaskGroup is used to set the job for selection
	SetJob(job *structs.Job)

	// Select is used to select a node for the task group
	Select(tg *structs.TaskGroup) (*RankedNode, *structs.Resources)
}

// GenericStack is the Stack used for the Generic scheduler. It is
// designed to make better placement decisions at the cost of performance.
type GenericStack struct {
	batch               bool
	ctx                 Context
	source              *StaticIterator
	jobConstraint       *ConstraintIterator
	taskGroupDrivers    *DriverIterator
	taskGroupConstraint *ConstraintIterator
	binPack             *BinPackIterator
	jobAntiAff          *JobAntiAffinityIterator
	limit               *LimitIterator
	maxScore            *MaxScoreIterator
}

// NewGenericStack constructs a stack used for selecting service placements
func NewGenericStack(batch bool, ctx Context, baseNodes []*structs.Node) *GenericStack {
	// Create a new stack
	s := &GenericStack{
		batch: batch,
		ctx:   ctx,
	}

	// Create the source iterator. We randomize the order we visit nodes
	// to reduce collisions between schedulers and to do a basic load
	// balancing across eligible nodes.
	s.source = NewRandomIterator(ctx, baseNodes)

	// Attach the job constraints. The job is filled in later.
	s.jobConstraint = NewConstraintIterator(ctx, s.source, nil)

	// Filter on task group drivers first as they are faster
	s.taskGroupDrivers = NewDriverIterator(ctx, s.jobConstraint, nil)

	// Filter on task group constraints second
	s.taskGroupConstraint = NewConstraintIterator(ctx, s.taskGroupDrivers, nil)

	// Upgrade from feasible to rank iterator
	rankSource := NewFeasibleRankIterator(ctx, s.taskGroupConstraint)

	// Apply the bin packing, this depends on the resources needed
	// by a particular task group. Only enable eviction for the service
	// scheduler as that logic is expensive.
	evict := !batch
	s.binPack = NewBinPackIterator(ctx, rankSource, evict, 0)

	// Apply the job anti-affinity iterator. This is to avoid placing
	// multiple allocations on the same node for this job. The penalty
	// is less for batch jobs as it matters less.
	penalty := serviceJobAntiAffinityPenalty
	if batch {
		penalty = batchJobAntiAffinityPenalty
	}
	s.jobAntiAff = NewJobAntiAffinityIterator(ctx, s.binPack, penalty, "")

	// Apply a limit function. This is to avoid scanning *every* possible node.
	s.limit = NewLimitIterator(ctx, s.binPack, 2)

	// Select the node with the maximum score for placement
	s.maxScore = NewMaxScoreIterator(ctx, s.limit)

	// Set the nodes if given
	if len(baseNodes) != 0 {
		s.SetNodes(baseNodes)
	}
	return s
}

func (s *GenericStack) SetNodes(baseNodes []*structs.Node) {
	// Shuffle base nodes
	shuffleNodes(baseNodes)

	// Update the set of base nodes
	s.source.SetNodes(baseNodes)

	// Apply a limit function. This is to avoid scanning *every* possible node.
	// For batch jobs we only need to evaluate 2 options and depend on the
	// powwer of two choices. For services jobs we need to visit "enough".
	// Using a log of the total number of nodes is a good restriction, with
	// at least 2 as the floor
	limit := 2
	if n := len(baseNodes); !s.batch && n > 0 {
		logLimit := int(math.Ceil(math.Log2(float64(n))))
		if logLimit > limit {
			limit = logLimit
		}
	}
	s.limit.SetLimit(limit)
}

func (s *GenericStack) SetJob(job *structs.Job) {
	s.jobConstraint.SetConstraints(job.Constraints)
	s.binPack.SetPriority(job.Priority)
	s.jobAntiAff.SetJob(job.ID)
}

func (s *GenericStack) Select(tg *structs.TaskGroup) (*RankedNode, *structs.Resources) {
	// Reset the max selector and context
	s.maxScore.Reset()
	s.ctx.Reset()
	start := time.Now()

	// Collect the constraints, drivers and resources required by each
	// sub-task to aggregate the TaskGroup totals
	constr := make([]*structs.Constraint, 0, len(tg.Constraints))
	drivers := make(map[string]struct{})
	size := new(structs.Resources)
	constr = append(constr, tg.Constraints...)
	for _, task := range tg.Tasks {
		drivers[task.Driver] = struct{}{}
		constr = append(constr, task.Constraints...)
		size.Add(task.Resources)
	}

	// Update the parameters of iterators
	s.taskGroupDrivers.SetDrivers(drivers)
	s.taskGroupConstraint.SetConstraints(constr)
	s.binPack.SetTasks(tg.Tasks)

	// Find the node with the max score
	option := s.maxScore.Next()

	// Ensure that the task resources were specified
	if option != nil && len(option.TaskResources) != len(tg.Tasks) {
		for _, task := range tg.Tasks {
			option.SetTaskResources(task, task.Resources)
		}
	}

	// Store the compute time
	s.ctx.Metrics().AllocationTime = time.Since(start)
	return option, size
}
