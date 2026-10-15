package libver

import "strings"

// The curated list: vendor SDKs (cloud, payments, issue trackers, observability, AI, auth, messaging, data) and the
// frameworks and toolkits whose API changes between major versions. A name ending in "*" is a prefix. npm and Go names
// match exactly as written; NuGet and PyPI names match without regard to case (and PyPI treats -, _ and . alike).
// Adding a name here is the whole change for a new SDK.
var curated = map[string][]string{
	NPM: {
		// payments, commerce
		"stripe", "@stripe/*", "@paypal/*", "@lemonsqueezy/*", "@shopify/*",
		// issue trackers, docs, collaboration
		"@linear/sdk", "@notionhq/client", "@octokit/*", "octokit", "@slack/*", "discord.js", "@atlaskit/*", "jira.js",
		// Microsoft
		"@azure/*", "@microsoft/*", "@fluentui/*", "@azure-rest/*", "applicationinsights",
		// other clouds and platforms
		"@aws-sdk/*", "aws-sdk", "@google-cloud/*", "googleapis", "firebase", "firebase-admin", "@firebase/*",
		"@cloudflare/*", "wrangler", "@vercel/*", "@netlify/*", "@supabase/*", "@neondatabase/*", "@planetscale/*", "@upstash/*",
		"@sentry/*", "@datadog/*", "dd-trace", "@opentelemetry/*", "posthog-js", "posthog-node",
		// AI
		"openai", "@anthropic-ai/*", "@google/genai", "@google/generative-ai", "@mistralai/*", "ai", "@ai-sdk/*", "langchain",
		"@langchain/*", "@modelcontextprotocol/*", "@huggingface/*",
		// auth, messaging, email
		"@clerk/*", "@auth0/*", "next-auth", "@auth/*", "better-auth", "twilio", "@sendgrid/*", "resend", "postmark",
		// frameworks and toolkits
		"react", "react-dom", "next", "@remix-run/*", "remix", "react-router", "vue", "nuxt", "svelte", "@sveltejs/kit",
		"astro", "@angular/core", "solid-js", "hono", "express", "fastify", "@nestjs/core", "electron", "react-native", "expo",
		"tailwindcss", "vite", "typescript", "zod", "@tanstack/*", "@trpc/*", "graphql", "@apollo/client", "@apollo/server",
		"prisma", "@prisma/client", "drizzle-orm", "mongoose", "typeorm", "sequelize", "kysely", "vitest", "jest", "@playwright/test",
	},
	Go: {
		"github.com/stripe/stripe-go*", "github.com/aws/aws-sdk-go*", "cloud.google.com/go*", "google.golang.org/api",
		"github.com/Azure/azure-sdk-for-go*", "github.com/microsoftgraph/*", "github.com/microsoft/*", "github.com/getsentry/sentry-go",
		"github.com/openai/openai-go*", "github.com/anthropics/anthropic-sdk-go", "github.com/google/go-github*",
		"github.com/cloudflare/cloudflare-go*", "github.com/slack-go/slack", "github.com/twilio/*", "github.com/sendgrid/*",
		"github.com/modelcontextprotocol/go-sdk", "github.com/mark3labs/mcp-go", "go.opentelemetry.io/otel*",
		"github.com/DataDog/dd-trace-go*", "firebase.google.com/go*", "github.com/supabase-community/*",
		"github.com/gin-gonic/gin", "github.com/labstack/echo*", "github.com/gofiber/fiber*", "github.com/go-chi/chi*",
		"connectrpc.com/connect", "google.golang.org/grpc", "google.golang.org/protobuf", "gorm.io/gorm", "github.com/jackc/pgx*",
		"entgo.io/ent", "github.com/wailsapp/wails*", "github.com/spf13/cobra",
	},
	NuGet: {
		"Microsoft.*", "Azure.*", "Stripe.net", "Sentry*", "OpenAI", "Azure.AI.OpenAI", "Anthropic*", "AWSSDK.*", "Google.Cloud.*",
		"Google.Apis*", "Octokit", "Twilio", "SendGrid", "Slack*", "Npgsql*", "Dapper", "MediatR", "FluentValidation", "Serilog*",
		"OpenTelemetry*", "Polly", "MassTransit*", "Hangfire*", "AutoMapper", "xunit*", "NUnit*", "Moq",
	},
	PyPI: {
		"stripe", "boto3", "botocore", "aioboto3", "azure-*", "msgraph-sdk", "msal", "google-cloud-*", "google-api-python-client",
		"google-genai", "google-generativeai", "firebase-admin", "openai", "anthropic", "mistralai", "cohere", "langchain*",
		"llama-index*", "transformers", "huggingface-hub", "mcp", "sentry-sdk", "ddtrace", "opentelemetry-*", "notion-client",
		"linear-sdk", "slack-sdk", "slack-bolt", "twilio", "sendgrid", "pygithub", "supabase", "django", "djangorestframework",
		"fastapi", "flask", "starlette", "pydantic", "sqlalchemy", "alembic", "celery", "pandas", "numpy", "polars", "torch",
		"pytest", "httpx", "requests",
	},
}

// Curated reports whether a library of the ecosystem is on the curated list.
func Curated(eco, name string) bool {
	norm := func(s string) string {
		switch eco {
		case NuGet:
			return strings.ToLower(s)
		case PyPI:
			return pyNorm(s)
		}
		return s
	}
	n := norm(name)
	for _, pat := range curated[eco] {
		if prefix, ok := strings.CutSuffix(pat, "*"); ok {
			if strings.HasPrefix(n, norm(prefix)) {
				return true
			}
		} else if n == norm(pat) {
			return true
		}
	}
	return false
}
