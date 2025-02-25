/*
Copyright (c) 2021 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package operatorroles

import (
	"fmt"
	"os"
	"strings"
	"time"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/rosa/pkg/helper"
	"github.com/openshift/rosa/pkg/helper/roles"
	"github.com/openshift/rosa/pkg/rosa"
	"github.com/spf13/cobra"

	"github.com/openshift/rosa/pkg/aws"
	awscb "github.com/openshift/rosa/pkg/aws/commandbuilder"
	"github.com/openshift/rosa/pkg/aws/tags"
	"github.com/openshift/rosa/pkg/interactive"
	"github.com/openshift/rosa/pkg/interactive/confirm"
	"github.com/openshift/rosa/pkg/ocm"
	rprtr "github.com/openshift/rosa/pkg/reporter"
)

var args struct {
	upgradeVersion string
}

var Cmd = &cobra.Command{
	Use:     "operator-roles",
	Aliases: []string{"operator-role", "operatorroles"},
	Short:   "Upgrade operator IAM roles for a cluster.",
	Long:    "Upgrade cluster-specific operator IAM roles to latest version.",
	Example: `  # Upgrade cluster-specific operator IAM roles
  rosa upgrade operators-roles`,
	RunE: run,
}

func init() {
	flags := Cmd.Flags()

	aws.AddModeFlag(Cmd)
	ocm.AddClusterFlag(Cmd)

	flags.StringVar(
		&args.upgradeVersion,
		"version",
		"",
		"Version of OpenShift that the cluster will be upgraded to",
	)

	confirm.AddFlag(flags)
	interactive.AddFlag(flags)
}

func run(cmd *cobra.Command, argv []string) error {
	r := rosa.NewRuntime().WithAWS().WithOCM()
	defer r.Cleanup()

	mode, err := aws.GetMode()
	if err != nil {
		r.Reporter.Errorf("%s", err)
		os.Exit(1)
	}

	clusterKey := r.GetClusterKey()

	defaultPolicyVersion, err := r.OCMClient.GetDefaultVersion()
	if err != nil {
		r.Reporter.Errorf("Error getting latest default version: %s", err)
		os.Exit(1)
	}

	cluster := r.FetchCluster()
	/**
	we dont want to give this option to the end-user. Adding this as a support for srep if needed.
	*/
	if args.upgradeVersion != "" {
		version := args.upgradeVersion
		availableUpgrades, err := r.OCMClient.GetAvailableUpgrades(ocm.GetVersionID(cluster))
		if err != nil {
			r.Reporter.Errorf("Failed to find available upgrades: %v", err)
			os.Exit(1)
		}
		if len(availableUpgrades) == 0 {
			r.Reporter.Warnf("There are no available upgrades")
			os.Exit(0)
		}
		// Check that the version is valid
		validVersion := false
		for _, v := range availableUpgrades {
			if v == version {
				validVersion = true
				break
			}
		}
		if !validVersion {
			r.Reporter.Errorf("Expected a valid version to upgrade the cluster")
			os.Exit(1)
		}
	}

	operatorRoles, hasOperatorRoles := cluster.AWS().STS().GetOperatorIAMRoles()
	if !hasOperatorRoles || len(operatorRoles) == 0 {
		r.Reporter.Errorf("Cluster '%s' doesnt have any operator roles associated with it",
			clusterKey)
	}

	prefix, err := aws.GetPrefixFromInstallerAccountRole(cluster)
	if err != nil {
		r.Reporter.Errorf("Error getting account role prefix for the cluster '%s'",
			clusterKey)
	}
	unifiedPath, err := aws.GetPathFromAccountRole(cluster, aws.AccountRoles[aws.InstallerAccountRole].Name)
	if err != nil {
		r.Reporter.Errorf("Expected a valid path for '%s': %v", cluster.AWS().STS().RoleARN(), err)
		os.Exit(1)
	}

	isAccountRoleUpgradeNeed := false
	// If this is invoked from upgrade cluster then we already performed upgrade account roles

	//Check if account roles are up-to-date
	isAccountRoleUpgradeNeed, err = r.AWSClient.IsUpgradedNeededForAccountRolePolicies(
		prefix, defaultPolicyVersion)
	if err != nil {
		r.Reporter.Errorf("%s", err)
		os.Exit(1)
	}
	if isAccountRoleUpgradeNeed {
		r.Reporter.Infof("Account roles with prefix '%s' need to be upgraded before operator roles. "+
			"Roles can be upgraded with the following command :"+
			"\n\n\trosa upgrade account-roles --prefix %s\n", prefix, prefix)
		os.Exit(1)
	}

	credRequests, err := r.OCMClient.GetCredRequests(cluster.Hypershift().Enabled())
	if err != nil {
		r.Reporter.Errorf("Error getting operator credential request from OCM %s", err)
		os.Exit(1)
	}

	isOperatorPolicyUpgradeNeeded, err := r.AWSClient.IsUpgradedNeededForOperatorRolePoliciesUsingPrefix(prefix,
		r.Creator.AccountID, defaultPolicyVersion, credRequests, unifiedPath)
	if err != nil {
		r.Reporter.Errorf("%s", err)
		os.Exit(1)
	}

	version := args.upgradeVersion
	if version == "" {
		version = cluster.Version().RawID()
	}

	//Check if the upgrade is needed for the operators
	missingRolesInCS, err := r.OCMClient.FindMissingOperatorRolesForUpgrade(cluster, version)
	if err != nil {
		return err
	}

	if len(missingRolesInCS) <= 0 && !isOperatorPolicyUpgradeNeeded {
		r.Reporter.Infof("Operator roles associated with the cluster '%s' are already up-to-date.", cluster.ID())
		return nil
	}

	if len(missingRolesInCS) > 0 || isOperatorPolicyUpgradeNeeded {
		r.Reporter.Infof("Starting to upgrade the operator IAM roles and policies")
	}
	// Determine if interactive mode is needed
	if !interactive.Enabled() && !cmd.Flags().Changed("mode") {
		interactive.Enable()
	}
	policies, err := r.OCMClient.GetPolicies("OperatorRole")
	if err != nil {
		r.Reporter.Errorf("Expected a valid role creation mode: %s", err)
		os.Exit(1)
	}

	env, err := ocm.GetEnv()
	if err != nil {
		r.Reporter.Errorf("Failed to determine OCM environment: %v", err)
		os.Exit(1)
	}

	mode, err = handleModeFlag(cmd, mode, err, r.Reporter)
	if err != nil {
		r.Reporter.Errorf("%s", err)
		os.Exit(1)
	}

	if isOperatorPolicyUpgradeNeeded {
		err = upgradeOperatorPolicies(mode, r, prefix, isAccountRoleUpgradeNeed,
			policies, env, defaultPolicyVersion, credRequests, cluster, unifiedPath)
		if err != nil {
			r.Reporter.Errorf("%s", err)
			os.Exit(1)
		}
	}

	if len(missingRolesInCS) > 0 {
		createdMissingRoles := 0
		for _, operator := range missingRolesInCS {
			roleName := roles.GetOperatorRoleName(cluster, operator)
			exists, _, err := r.AWSClient.CheckRoleExists(roleName)
			if err != nil {
				return r.Reporter.Errorf("Error when detecting checking missing operator IAM roles %s", err)
			}
			if !exists {
				err = createOperatorRole(mode, r, cluster, prefix, missingRolesInCS, policies, unifiedPath)
				if err != nil {
					r.Reporter.Errorf("%s", err)
					os.Exit(1)
				}
				createdMissingRoles++
			}
		}
		if createdMissingRoles == 0 {
			r.Reporter.Infof(
				"Missing roles/policies have already been created. Please continue with cluster upgrade process.",
			)
		}
	}
	return nil
}

func handleModeFlag(cmd *cobra.Command, mode string, err error,
	reporter *rprtr.Object) (string, error) {
	if interactive.Enabled() {
		mode, err = interactive.GetOption(interactive.Input{
			Question: "Operator IAM role/policy upgrade mode",
			Help:     cmd.Flags().Lookup("mode").Usage,
			Default:  aws.ModeAuto,
			Options:  aws.Modes,
			Required: true,
		})
		if err != nil {
			reporter.Errorf("Expected a valid operator IAM role upgrade mode: %s", err)
			os.Exit(1)
		}
	}
	aws.SetModeKey(mode)
	return mode, err
}

func upgradeOperatorPolicies(mode string, r *rosa.Runtime,
	prefix string, isAccountRoleUpgradeNeed bool, policies map[string]string, env string, defaultPolicyVersion string,
	credRequests map[string]*cmv1.STSOperator, cluster *cmv1.Cluster, policyPath string) error {
	switch mode {
	case aws.ModeAuto:
		if !confirm.Prompt(true, "Upgrade the operator role policy to version %s?", defaultPolicyVersion) {
			return nil
		}
		err := aws.UpgradeOperatorRolePolicies(r.Reporter, r.AWSClient, r.Creator.AccountID, prefix, policies,
			defaultPolicyVersion, credRequests, policyPath)
		if err != nil {
			if strings.Contains(err.Error(), "Throttling") {
				r.OCMClient.LogEvent("ROSAUpgradeOperatorRolesModeAuto", map[string]string{
					ocm.Response:   ocm.Failure,
					ocm.Version:    defaultPolicyVersion,
					ocm.IsThrottle: "true",
				})
			}
			return r.Reporter.Errorf("Error upgrading the operator policies: %s", err)
		}
		return nil
	case aws.ModeManual:
		err := aws.GeneratePolicyFiles(r.Reporter, env, false,
			true, policies, credRequests)
		if err != nil {
			r.Reporter.Errorf("There was an error generating the policy files: %s", err)
			os.Exit(1)
		}

		if r.Reporter.IsTerminal() {
			r.Reporter.Infof("All policy files saved to the current directory")
			r.Reporter.Infof("Run the following commands to upgrade the operator IAM policies:\n")
			if isAccountRoleUpgradeNeed {
				r.Reporter.Warnf("Operator role policies MUST only be upgraded after " +
					"Account Role policies upgrade has completed.\n")
			}
		}
		commands := aws.BuildOperatorRoleCommands(prefix, r.Creator.AccountID, r.AWSClient,
			defaultPolicyVersion, credRequests, policyPath)
		fmt.Println(awscb.JoinCommands(commands))
	default:
		return r.Reporter.Errorf("Invalid mode. Allowed values are %s", aws.Modes)
	}
	return nil
}

func createOperatorRole(
	mode string, r *rosa.Runtime, cluster *cmv1.Cluster, prefix string,
	missingRoles map[string]*cmv1.STSOperator, policies map[string]string, unifiedPath string) error {
	accountID := r.Creator.AccountID
	switch mode {
	case aws.ModeAuto:
		err := upgradeMissingOperatorRole(missingRoles, cluster, accountID, prefix, r,
			policies, unifiedPath)
		if err != nil {
			return err
		}
		helper.DisplaySpinnerWithDelay(r.Reporter, "Waiting for operator roles to reconcile", 5*time.Second)
	case aws.ModeManual:
		commands, err := roles.BuildMissingOperatorRoleCommand(
			missingRoles, cluster, accountID, r, policies, unifiedPath, prefix)
		if err != nil {
			return err
		}
		if r.Reporter.IsTerminal() {
			r.Reporter.Infof("Run the following commands to create the operator roles:\n")
		}
		fmt.Println(commands)
	default:
		r.Reporter.Errorf("Invalid mode. Allowed values are %s", aws.Modes)
		os.Exit(1)
	}
	return nil
}

func upgradeMissingOperatorRole(missingRoles map[string]*cmv1.STSOperator, cluster *cmv1.Cluster,
	accountID string, prefix string, r *rosa.Runtime, policies map[string]string,
	unifiedPath string) error {
	for _, operator := range missingRoles {
		roleName := roles.GetOperatorRoleName(cluster, operator)
		if !confirm.Prompt(true, "Create the '%s' role?", roleName) {
			continue
		}
		policyDetails := policies["operator_iam_role_policy"]

		policyARN := aws.GetOperatorPolicyARN(accountID, prefix, operator.Namespace(), operator.Name(), unifiedPath)
		policy, err := aws.GenerateOperatorRolePolicyDoc(cluster, accountID, operator, policyDetails)
		if err != nil {
			return err
		}
		r.Reporter.Debugf("Creating role '%s'", roleName)
		roleARN, err := r.AWSClient.EnsureRole(roleName, policy, "", "",
			map[string]string{
				tags.ClusterID:         cluster.ID(),
				tags.OperatorNamespace: operator.Namespace(),
				tags.OperatorName:      operator.Name(),
				tags.RedHatManaged:     "true",
			}, unifiedPath)
		if err != nil {
			return err
		}
		r.Reporter.Infof("Created role '%s' with ARN '%s'", roleName, roleARN)
		r.Reporter.Debugf("Attaching permission policy '%s' to role '%s'", policyARN, roleName)
		err = r.AWSClient.AttachRolePolicy(roleName, policyARN)
		if err != nil {
			return fmt.Errorf("Failed to attach role policy. Check your prefix or run "+
				"'rosa create account-roles' to create the necessary policies: %s", err)
		}
	}
	return nil
}
