package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
)

// LifecycleTransition is transition value.
const LifecycleTransition = "autoscaling:EC2_INSTANCE_TERMINATING"

func main() {
	if strings.HasPrefix(os.Getenv("AWS_EXECUTION_ENV"), "AWS_Lambda") || os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		lambda.Start(handler)
	} else {
		runAsCLI()
	}
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
		log.Println(err.Error())
		os.Exit(1)
	}
}

func handler(ctx context.Context, event *events.AutoScalingEvent) error {
	sess, err := session.NewSession(&aws.Config{})
	if err != nil {
		return err
	}
	svc := ecs.New(sess)
	log.Printf("event: %#v", event)

	asgName := event.Detail["AutoScalingGroupName"].(string)
	instanceID := event.Detail["EC2InstanceId"].(string)
	transition := event.Detail["LifecycleTransition"].(string)
	log.Printf("[info] starting lifecycle hook AutoScalingGroupName:%s EC2InstanceId:%s LifecycleTransition:%s", asgName, instanceID, transition)

	if transition != LifecycleTransition {
		return fmt.Errorf("[error] unexpected transision: %s", transition)
	}

	// determine the container instance arn by EC2 Instance ID
	cluster := aws.String(asgName)
	list, err := svc.ListContainerInstances(&ecs.ListContainerInstancesInput{
		Cluster: cluster, // ASG name == Cluster name
	})
	if err != nil {
		return err
	}
	desc, err := svc.DescribeContainerInstances(&ecs.DescribeContainerInstancesInput{
		Cluster:            cluster,
		ContainerInstances: list.ContainerInstanceArns,
	})
	if err != nil {
		return err
	}
	var containerInstanceArn *string
	for _, c := range desc.ContainerInstances {
		if *c.Ec2InstanceId == instanceID {
			containerInstanceArn = c.ContainerInstanceArn
			break
		}
	}
	if containerInstanceArn == nil {
		return fmt.Errorf("[error] cloud not determine container instance which has %s", instanceID)
	}

	log.Printf("[info] container instance %s", *containerInstanceArn)

	// draining
	_, err = svc.UpdateContainerInstancesState(&ecs.UpdateContainerInstancesStateInput{
		Cluster:            cluster,
		Status:             aws.String("DRAINING"),
		ContainerInstances: []*string{containerInstanceArn},
	})
	if err != nil {
		return err
	}
	log.Printf("[info] complete UpdateContainerInstancesState(%s) to draining", *containerInstanceArn)

	// wait for all tasks exited
	for {
		tasks, err := svc.ListTasks(&ecs.ListTasksInput{
			Cluster:           cluster,
			ContainerInstance: containerInstanceArn,
		})
		if err != nil {
			log.Printf("[warn] %s", err)
			time.Sleep(10 * time.Second)
			continue
		}
		if len(tasks.TaskArns) == 0 {
			break
		}
		log.Printf("[info] %d tasks still running on %s", len(tasks.TaskArns), *containerInstanceArn)
		time.Sleep(10 * time.Second)
	}
	log.Printf("[info] all tasks exited on %s", *containerInstanceArn)

	// TODO complete-lifecycle-action
	return nil
}
