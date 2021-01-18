package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ecs"
)

// LifecycleTransition is transition value.
const LifecycleTransition = "autoscaling:EC2_INSTANCE_TERMINATING"

func main() {
	if strings.HasPrefix(os.Getenv("AWS_EXECUTION_ENV"), "AWS_Lambda") || os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		runAsLambda()
	} else {
		runAsCLI()
	}
}

func runAsLambda() {
	lambda.Start(func(ctx context.Context, event *events.AutoScalingEvent) error {
		if err := handler(ctx, event); err != nil {
			log.Println("[error]", err)
			return err
		}
		return nil
	})
}

func runAsCLI() {
	var asgName, instanceID string
	flag.StringVar(&asgName, "asg-name", "", "AutoScalingGroupName")
	flag.StringVar(&instanceID, "instance-id", "", "EC2InstanceId")
	flag.Parse()
	if asgName == "" || instanceID == "" {
		flag.Usage()
		return
	}

	event := &events.AutoScalingEvent{
		Detail: map[string]interface{}{
			"AutoScalingGroupName": asgName,
			"EC2InstanceId":        instanceID,
			"LifecycleTransition":  LifecycleTransition,
		},
	}
	if err := handler(context.Background(), event); err != nil {
		log.Println("[error]", err)
		os.Exit(1)
	}
}

func handler(ctx context.Context, event *events.AutoScalingEvent) error {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		return err
	}
	ecsSvc := ecs.New(sess)
	asgSvc := autoscaling.New(sess)
	log.Printf("[info] event: %#v", event)

	asgName := str(event.Detail["AutoScalingGroupName"])
	instanceID := str(event.Detail["EC2InstanceId"])
	transition := str(event.Detail["LifecycleTransition"])
	log.Printf("[info] starting lifecycle hook AutoScalingGroupName:%s EC2InstanceId:%s LifecycleTransition:%s", asgName, instanceID, transition)

	if transition != LifecycleTransition {
		return fmt.Errorf("unexpected transision: %s", transition)
	}
	cluster, err := detectECSCluster(asgSvc, asgName)
	if err != nil {
		return err
	}
	if err := drainingInstance(ecsSvc, cluster, instanceID); err != nil {
		return err
	}
	if err := complate(asgSvc, event); err != nil {
		return err
	}

	return nil
}

func drainingInstance(svc *ecs.ECS, cluster, instanceID string) error {
	// determine the container instance arn by EC2 Instance ID
	list, err := svc.ListContainerInstances(&ecs.ListContainerInstancesInput{
		Cluster: &cluster,
	})
	if err != nil {
		return err
	}
	desc, err := svc.DescribeContainerInstances(&ecs.DescribeContainerInstancesInput{
		Cluster:            &cluster,
		ContainerInstances: list.ContainerInstanceArns,
	})
	if err != nil {
		return err
	}
	var containerInstanceArn string
	for _, c := range desc.ContainerInstances {
		if aws.StringValue(c.Ec2InstanceId) == instanceID {
			containerInstanceArn = aws.StringValue(c.ContainerInstanceArn)
			break
		}
	}
	if containerInstanceArn == "" {
		return fmt.Errorf("could not determine container instance which has %s", instanceID)
	}

	log.Printf("[info] container instance %s", containerInstanceArn)

	// draining
	_, err = svc.UpdateContainerInstancesState(&ecs.UpdateContainerInstancesStateInput{
		Cluster:            &cluster,
		Status:             aws.String("DRAINING"),
		ContainerInstances: []*string{&containerInstanceArn},
	})
	if err != nil {
		return err
	}
	log.Printf("[info] complete UpdateContainerInstancesState(%s) to draining", containerInstanceArn)

	// wait for all tasks exited
	for {
		time.Sleep(10 * time.Second)
		tasks, err := svc.ListTasks(&ecs.ListTasksInput{
			Cluster:           &cluster,
			ContainerInstance: &containerInstanceArn,
		})
		if err != nil {
			log.Printf("[warn] %s", err)
			continue
		}
		if len(tasks.TaskArns) == 0 {
			break
		}
		log.Printf(
			"[info] %d tasks still running on %s %s",
			len(tasks.TaskArns),
			instanceID,
			containerInstanceArn,
		)
	}
	log.Printf("[info] all tasks exited on %s %s", instanceID, containerInstanceArn)
	return nil
}

func detectECSCluster(svc *autoscaling.AutoScaling, asgName string) (string, error) {
	var cluster string
	out, err := svc.DescribeAutoScalingGroups(&autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(asgName)},
	})
	if err != nil {
		return "", err
	}
	if len(out.AutoScalingGroups) == 0 {
		return "", fmt.Errorf("AutoScalingGroup name %s is not found", asgName)
	}
	for _, tag := range out.AutoScalingGroups[0].Tags {
		if strings.ToLower(aws.StringValue(tag.Key)) == "cluster" {
			cluster = aws.StringValue(tag.Value)
		}
	}
	if cluster == "" {
		return "", errors.New("failed to detect cluster from ASG tag Key=cluster")
	}
	log.Printf("[info] ECS cluster detected: %s", cluster)
	return cluster, nil
}

func complate(svc *autoscaling.AutoScaling, event *events.AutoScalingEvent) error {
	if event.Detail["LifecycleActionToken"] == nil {
		log.Println("[info] skip complete")
		return nil
	}
	_, err := svc.CompleteLifecycleAction(&autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  aws.String(str(event.Detail["AutoScalingGroupName"])),
		InstanceId:            aws.String(str(event.Detail["EC2InstanceId"])),
		LifecycleActionResult: aws.String("CONTINUE"),
		LifecycleActionToken:  aws.String(str(event.Detail["LifecycleActionToken"])),
		LifecycleHookName:     aws.String(str(event.Detail["LifecycleHookName"])),
	})
	return err
}

func str(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
