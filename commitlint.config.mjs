export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    "scope-enum": [
      2,
      "always",
      [
        "repo",
        "ci",
        "db",
        "contracts",
        "geo",
        "ingest",
        "alert",
        "notifier",
        "realtime",
        "core",
        "web",
        "mobile",
        "ml",
        "assistant",
        "deploy",
        "docs",
        "deps",
      ],
    ],
  },
};
