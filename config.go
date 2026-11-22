package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port             string
	DatabaseURL      string
	VerifyToken      string
	AppSecret        string
	IGAccessToken    string
	GraphHost        string
	GraphVersion     string
	ReplyEnabled     bool
	DryRun           bool
	TestMode         bool
	AdminToken       string
	CronSecret       string
	BackgroundWorker bool
	Workers          int
	MinSendInterval  time.Duration
	RunMaxDuration   time.Duration
	MaxAttempts      int
	QuestionMode     string
	AIEnabled        bool
	AIKey            string
	AIModel          string
	AIDailyLimit     int
	RepliesFile      string
}

func env(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(env(key, ""))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes"
}

func envInt(key string, def int) int {
	v := env(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("sozlama: %s qiymati noto'g'ri, standart qiymat ishlatildi", key)
		return def
	}
	return n
}

func loadConfig() *Config {
	return &Config{
		Port:             env("PORT", "8080"),
		DatabaseURL:      env("DATABASE_URL", ""),
		VerifyToken:      env("VERIFY_TOKEN", ""),
		AppSecret:        env("APP_SECRET", ""),
		IGAccessToken:    env("IG_ACCESS_TOKEN", ""),
		GraphHost:        env("GRAPH_HOST", "graph.instagram.com"),
		GraphVersion:     env("GRAPH_VERSION", "v26.0"),
		ReplyEnabled:     envBool("REPLY_ENABLED", true),
		DryRun:           envBool("DRY_RUN", true),
		TestMode:         envBool("TEST_MODE", false),
		AdminToken:       env("ADMIN_TOKEN", ""),
		CronSecret:       env("CRON_SECRET", ""),
		BackgroundWorker: envBool("BACKGROUND_WORKER", false),
		Workers:          envInt("WORKERS", 3),
		MinSendInterval:  time.Duration(envInt("MIN_SEND_INTERVAL_MS", 3000)) * time.Millisecond,
		RunMaxDuration:   time.Duration(envInt("RUN_MAX_SECONDS", 50)) * time.Second,
		MaxAttempts:      envInt("MAX_ATTEMPTS", 5),
		QuestionMode:     strings.ToLower(env("QUESTION_MODE", "bank")),
		AIEnabled:        envBool("AI_ENABLED", false),
		AIKey:            env("AI_API_KEY", ""),
		AIModel:          env("AI_MODEL", ""),
		AIDailyLimit:     envInt("AI_DAILY_LIMIT", 200),
		RepliesFile:      env("REPLIES_FILE", ""),
	}
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.VerifyToken == "" {
		missing = append(missing, "VERIFY_TOKEN")
	}
	if c.AppSecret == "" {
		missing = append(missing, "APP_SECRET")
	}
	if c.CronSecret == "" {
		missing = append(missing, "CRON_SECRET")
	}
	if c.AdminToken == "" {
		missing = append(missing, "ADMIN_TOKEN")
	}
	if !c.DryRun && c.IGAccessToken == "" {
		missing = append(missing, "IG_ACCESS_TOKEN")
	}
	if c.AIEnabled && (c.AIKey == "" || c.AIModel == "") {
		missing = append(missing, "AI_API_KEY va AI_MODEL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("quyidagi sozlamalar kiritilmagan: %s", strings.Join(missing, ", "))
	}
	if c.Workers < 1 {
		c.Workers = 1
	}
	if c.MaxAttempts < 1 {
		c.MaxAttempts = 1
	}
	return nil
}
