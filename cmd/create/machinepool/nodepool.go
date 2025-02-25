package machinepool

import (
	"os"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/rosa/pkg/interactive"
	"github.com/openshift/rosa/pkg/rosa"
	"github.com/spf13/cobra"
)

func addNodePool(cmd *cobra.Command, clusterKey string, cluster *cmv1.Cluster, r *rosa.Runtime) {
	var err error

	// TODO NodePool commands don't support (yet) some of the machinepool flags
	if cmd.Flags().Changed("multi-availability-zone") {
		r.Reporter.Errorf("Setting the `multi-availability-zone` flag is not yet supported for hosted clusters")
		os.Exit(1)
	}

	if cmd.Flags().Changed("availability-zone") {
		r.Reporter.Errorf("Setting the `availability-zone` flag is not yet supported for hosted clusters")
		os.Exit(1)
	}

	if cmd.Flags().Changed("subnet") {
		r.Reporter.Errorf("Setting the `subnet` flag is not yet supported for hosted clusters")
		os.Exit(1)
	}

	// Hosted clusters create identifiers for NodePools, users don't interact directly with these resources
	if cmd.Flags().Changed("name") {
		r.Reporter.Errorf("Setting the `name` is not supported for hosted clusters")
		os.Exit(1)
	}

	isMinReplicasSet := cmd.Flags().Changed("min-replicas")
	isMaxReplicasSet := cmd.Flags().Changed("max-replicas")
	isAutoscalingSet := cmd.Flags().Changed("enable-autoscaling")
	isReplicasSet := cmd.Flags().Changed("replicas")

	minReplicas := args.minReplicas
	maxReplicas := args.maxReplicas
	autoscaling := args.autoscalingEnabled
	replicas := args.replicas

	// Autoscaling
	if !isReplicasSet && !autoscaling && !isAutoscalingSet && interactive.Enabled() {
		autoscaling, err = interactive.GetBool(interactive.Input{
			Question: "Enable autoscaling",
			Help:     cmd.Flags().Lookup("enable-autoscaling").Usage,
			Default:  autoscaling,
			Required: false,
		})
		if err != nil {
			r.Reporter.Errorf("Expected a valid value for enable-autoscaling: %s", err)
			os.Exit(1)
		}
	}

	// TODO Update the autoscaling input validator when multi-AZ is implemented
	if autoscaling {
		// if the user set replicas and enabled autoscaling
		if isReplicasSet {
			r.Reporter.Errorf("Replicas can't be set when autoscaling is enabled")
			os.Exit(1)
		}
		if interactive.Enabled() || !isMinReplicasSet {
			minReplicas, err = interactive.GetInt(interactive.Input{
				Question: "Min replicas",
				Help:     cmd.Flags().Lookup("min-replicas").Usage,
				Default:  minReplicas,
				Required: true,
			})
			if err != nil {
				r.Reporter.Errorf("Expected a valid number of min replicas: %s", err)
				os.Exit(1)
			}
		}

		if interactive.Enabled() || !isMaxReplicasSet {
			maxReplicas, err = interactive.GetInt(interactive.Input{
				Question: "Max replicas",
				Help:     cmd.Flags().Lookup("max-replicas").Usage,
				Default:  maxReplicas,
				Required: true,
			})
			if err != nil {
				r.Reporter.Errorf("Expected a valid number of max replicas: %s", err)
				os.Exit(1)
			}
		}
	} else {
		// if the user set min/max replicas and hasn't enabled autoscaling
		if isMinReplicasSet || isMaxReplicasSet {
			r.Reporter.Errorf("Autoscaling must be enabled in order to set min and max replicas")
			os.Exit(1)
		}
		if interactive.Enabled() || !isReplicasSet {
			replicas, err = interactive.GetInt(interactive.Input{
				Question: "Replicas",
				Help:     cmd.Flags().Lookup("replicas").Usage,
				Default:  replicas,
				Required: true,
			})
			if err != nil {
				r.Reporter.Errorf("Expected a valid number of replicas: %s", err)
				os.Exit(1)
			}
		}
	}

	npBuilder := cmv1.NewNodePool()

	if autoscaling {
		npBuilder = npBuilder.Autoscaling(
			cmv1.NewNodePoolAutoscaling().
				MinReplica(minReplicas).
				MaxReplica(maxReplicas))
	} else {
		npBuilder = npBuilder.Replicas(replicas)
	}

	nodePool, err := npBuilder.Build()
	if err != nil {
		r.Reporter.Errorf("Failed to create machine pool for hosted cluster '%s': %v", clusterKey, err)
		os.Exit(1)
	}

	createdNodePool, err := r.OCMClient.CreateNodePool(cluster.ID(), nodePool)
	if err != nil {
		r.Reporter.Errorf("Failed to add machine pool to hosted cluster '%s': %v", clusterKey, err)
		os.Exit(1)
	}

	r.Reporter.Infof("Machine pool '%s' created successfully on hosted cluster '%s'", createdNodePool.ID(), clusterKey)
	r.Reporter.Infof("To view all machine pools, run 'rosa list machinepools -c %s'", clusterKey)
}
