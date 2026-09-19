/**
 * Schema dump tests (src/schema.ts).
 *
 * EXPECTED_JSON below is the schema format's version 2 (contract §25), whose
 * every rule is cross-language: the fragment subset and its key order, the
 * choices split, the selector encoding, the declared key order, the rewritten
 * defaults block, the behavioral-completeness keys and the byte canon. The
 * v1 delta this header used to record -- compound types as TS carrier schema
 * strings -- is GONE with the `type` key: a list carrier and a repeatable
 * scalar publish one array fragment now, whichever spelling declared them.
 *
 * Two TS-side deltas survive, and neither is a format rule:
 *  - dict flag defaults are emitted with sorted keys (the TS Map display
 *    convention; the mirror declared {"beta": 2, "alpha": 1}),
 *  - project_id is stripped (the written file adds it back from
 *    package.json).
 */

import { strict as assert } from "node:assert";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { deprecated } from "../src/factories.js";
import {
	type App,
	allOrNone,
	arg,
	choice,
	createApp,
	defineReadOnlyCommand,
	flag,
	flagSet,
	implies,
	memberChoiceFlag,
	readOnlyPassthrough,
	relativeToRoot,
	requires,
	t,
} from "../src/index.js";
import { schemaJson } from "../src/schema.js";
import { tempDir } from "./helpers.js";

const EXPECTED_JSON = `{
  "schema_version": 2,
  "defaults": {
    "schema_version": 2,
    "app": {
      "env_prefix": null,
      "config": false,
      "config_format": "json",
      "config_path": null,
      "config_conflict_mode": "cli-wins",
      "proc_observe_allowlist": [],
      "global_flags": [],
      "commands": {},
      "groups": {},
      "deprecated": {},
      "tag_contracts": {},
      "checks": {},
      "config_fields": {},
      "infra": {}
    },
    "flag": {
      "short": null,
      "env": null,
      "env_separator": null,
      "prefixed": true,
      "choices": null,
      "elect_by": null,
      "unique": false,
      "conflict_mode": null,
      "negatable": null,
      "nullable": false
    },
    "arg": {
      "variadic": false,
      "choices": null
    },
    "choice": {
      "flags": []
    },
    "choice_record": {
      "help": null
    },
    "command": {
      "consequential": false,
      "dry_run_supported": true,
      "dry_run_unsupported_reason": null,
      "update_of": null,
      "write_mode": null,
      "payload_schema": null,
      "owns_stdout": false,
      "passthrough": false,
      "flags": [],
      "flag_sets": [],
      "args": [],
      "tags": [],
      "constraints": [],
      "hidden": false,
      "interactive": false,
      "config_fields": [],
      "grants": [],
      "forwarding": null
    },
    "group": {
      "commands": {},
      "groups": {},
      "deprecated": {},
      "tags": [],
      "hidden": false
    },
    "config_field": {
      "default": null,
      "bound_commands": []
    },
    "check": {
      "scope": null
    },
    "infra": {
      "roots": [],
      "handshakes": [],
      "connections": []
    }
  },
  "name": "richapp",
  "version": "2.5.0",
  "help": "A comprehensive schema app",
  "env_prefix": "RICH",
  "config": true,
  "global_flags": [
    {
      "name": "chatter",
      "help": "Enable chatter output",
      "value_schema": {
        "type": "boolean"
      },
      "short": "V",
      "presence": "default",
      "default": false,
      "negatable": true
    },
    {
      "name": "log-level",
      "help": "Logging level",
      "value_schema": {
        "type": "string",
        "enum": [
          "debug",
          "info",
          "warn",
          "error"
        ]
      },
      "presence": "default",
      "default": "info",
      "env": "RICH_LOG_LEVEL",
      "choices": [
        {
          "value": "debug"
        },
        {
          "value": "info"
        },
        {
          "value": "warn"
        },
        {
          "value": "error"
        }
      ]
    },
    {
      "name": "state-file",
      "help": "State file relative to the infra root",
      "value_schema": {
        "type": "string"
      },
      "presence": "default",
      "default": {
        "relative_to_root": {
          "env_var": "RICH_HOME",
          "parts": [
            "state",
            "app.db"
          ]
        }
      }
    }
  ],
  "commands": {
    "check": {
      "name": "check",
      "help": "Run project checks registered via the check framework and report results",
      "effect": "read_only",
      "payload_schema": {
        "type": "array",
        "items": {
          "type": "object"
        }
      },
      "flags": [
        {
          "name": "all",
          "help": "Run every registered check regardless of tag or name filters",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "default",
          "default": false,
          "negatable": true
        },
        {
          "name": "tag",
          "help": "Tag DSL expression to select checks (e.g. 'changelog & !quality')",
          "value_schema": {
            "type": "string"
          },
          "presence": "default",
          "default": ""
        },
        {
          "name": "name",
          "help": "Glob pattern to filter checks by name (e.g. 'hash-*', '*coverage*')",
          "value_schema": {
            "type": "string"
          },
          "presence": "default",
          "default": ""
        },
        {
          "name": "list",
          "help": "List all registered checks with their tags and exit without running",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "default",
          "default": false,
          "negatable": true
        },
        {
          "name": "ignore-warnings",
          "help": "Treat warn-severity results as passing so they do not cause nonzero exit",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "default",
          "default": false,
          "negatable": true
        }
      ],
      "forwarding": {
        "reason": "framework-internal: absorbs app-defined global flag values"
      }
    },
    "types": {
      "name": "types",
      "help": "Test all flag types",
      "effect": "read_only",
      "flags": [
        {
          "name": "name",
          "help": "A string flag",
          "value_schema": {
            "type": "string"
          },
          "presence": "default",
          "default": "world"
        },
        {
          "name": "count",
          "help": "An integer flag",
          "value_schema": {
            "type": "integer"
          },
          "presence": "default",
          "default": 42
        },
        {
          "name": "big",
          "help": "A big integer flag",
          "value_schema": {
            "type": "integer"
          },
          "presence": "default",
          "default": 9007199254740993
        },
        {
          "name": "ratio",
          "help": "A float flag",
          "value_schema": {
            "type": "number"
          },
          "presence": "default",
          "default": 3.14
        },
        {
          "name": "sim-run",
          "help": "Dry run mode",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "required",
          "negatable": true
        },
        {
          "name": "cache-file",
          "help": "Cache file relative to the infra root",
          "value_schema": {
            "type": "string"
          },
          "presence": "default",
          "default": {
            "relative_to_root": {
              "env_var": "RICH_HOME",
              "parts": [
                "cache.bin"
              ]
            }
          }
        }
      ],
      "args": [
        {
          "name": "target",
          "help": "Target to process",
          "value_schema": {
            "type": "string"
          },
          "presence": "required"
        }
      ]
    },
    "multi": {
      "name": "multi",
      "help": "Test list and dict flags",
      "effect": "read_only",
      "flags": [
        {
          "name": "tag",
          "help": "Tags to apply",
          "value_schema": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "presence": "default",
          "default": [],
          "env": "RICH_TAGS",
          "env_separator": ",",
          "unique": true
        },
        {
          "name": "port",
          "help": "Ports to open",
          "value_schema": {
            "type": "array",
            "items": {
              "type": "integer"
            }
          },
          "presence": "default",
          "default": [
            80,
            443
          ]
        },
        {
          "name": "matrix",
          "help": "Named weights",
          "value_schema": {
            "type": "object",
            "additionalProperties": {
              "type": "integer"
            }
          },
          "presence": "default",
          "default": {
            "alpha": 1,
            "beta": 2
          }
        }
      ]
    },
    "output": {
      "name": "output",
      "help": "Test a member-spelled selector",
      "effect": "read_only",
      "flags": [
        {
          "name": "format",
          "help": "Output format",
          "presence": "required",
          "choices": [
            {
              "name": "as-json",
              "help": "JSON output"
            },
            {
              "name": "yaml",
              "help": "YAML output"
            },
            {
              "name": "text",
              "help": "Text output"
            }
          ],
          "elect_by": "member-flags"
        }
      ]
    },
    "deploy": {
      "name": "deploy",
      "help": "Test dependencies",
      "effect": "read_only",
      "flags": [
        {
          "name": "host",
          "help": "Deploy host",
          "value_schema": {
            "type": "string"
          },
          "presence": "optional"
        },
        {
          "name": "port-num",
          "help": "Deploy port",
          "value_schema": {
            "type": "integer"
          },
          "presence": "optional"
        },
        {
          "name": "ssl",
          "help": "Use SSL",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "required",
          "negatable": true
        },
        {
          "name": "cert",
          "help": "SSL certificate path",
          "value_schema": {
            "type": "string"
          },
          "presence": "optional"
        }
      ],
      "constraints": [
        {
          "type": "all_or_none",
          "name": "endpoint",
          "members": [
            {
              "kind": "flag",
              "name": "host",
              "when": "present"
            },
            {
              "kind": "flag",
              "name": "port-num",
              "when": "present"
            }
          ]
        },
        {
          "type": "requires",
          "name": "cert-ssl",
          "flag": "cert",
          "depends_on": "ssl"
        }
      ]
    },
    "notify": {
      "name": "notify",
      "help": "Test implies dependency",
      "effect": "read_only",
      "flags": [
        {
          "name": "email",
          "help": "Send email notification",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "required",
          "negatable": true
        },
        {
          "name": "alert",
          "help": "Enable alerts",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "required",
          "negatable": true
        }
      ],
      "constraints": [
        {
          "type": "implies",
          "name": "email-alert",
          "flag": "email",
          "implies": "alert",
          "value": true
        }
      ]
    },
    "query": {
      "name": "query",
      "help": "Test flag sets",
      "effect": "read_only",
      "flags": [
        {
          "name": "page",
          "help": "Page number",
          "value_schema": {
            "type": "integer"
          },
          "presence": "default",
          "default": 1
        },
        {
          "name": "per-page",
          "help": "Items per page",
          "value_schema": {
            "type": "integer"
          },
          "presence": "default",
          "default": 20
        }
      ],
      "flag_sets": [
        {
          "name": "pagination",
          "flags": [
            "page",
            "per-page"
          ]
        }
      ]
    },
    "files": {
      "name": "files",
      "help": "Test args",
      "effect": "read_only",
      "args": [
        {
          "name": "src",
          "help": "Source directory",
          "value_schema": {
            "type": "string"
          },
          "presence": "required"
        },
        {
          "name": "mode",
          "help": "Copy mode",
          "value_schema": {
            "type": "string"
          },
          "presence": "default",
          "default": "fast"
        },
        {
          "name": "extra",
          "help": "Extra files",
          "value_schema": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "presence": "optional",
          "variadic": true
        }
      ]
    },
    "exec": {
      "name": "exec",
      "help": "Execute a command",
      "effect": "read_only",
      "passthrough": true
    },
    "lint": {
      "name": "lint",
      "help": "Run linters",
      "effect": "read_only",
      "tags": [
        "ci",
        "quality"
      ]
    },
    "level": {
      "name": "level",
      "help": "Test int/float choices",
      "effect": "read_only",
      "flags": [
        {
          "name": "priority",
          "help": "Priority level",
          "value_schema": {
            "type": "integer",
            "enum": [
              1,
              2,
              3,
              4,
              5
            ]
          },
          "presence": "default",
          "default": 3,
          "choices": [
            {
              "value": 1
            },
            {
              "value": 2
            },
            {
              "value": 3
            },
            {
              "value": 4
            },
            {
              "value": 5
            }
          ]
        },
        {
          "name": "threshold",
          "help": "Threshold value",
          "value_schema": {
            "type": "number",
            "enum": [
              0.1,
              0.5,
              0.9
            ]
          },
          "presence": "default",
          "default": 0.5,
          "choices": [
            {
              "value": 0.1
            },
            {
              "value": 0.5
            },
            {
              "value": 0.9
            }
          ]
        }
      ]
    },
    "info": {
      "name": "info",
      "help": "Show info",
      "effect": "read_only",
      "flags": [
        {
          "name": "format",
          "help": "Output format",
          "value_schema": {
            "type": "string"
          },
          "short": "f",
          "presence": "default",
          "default": "table"
        },
        {
          "name": "color-off",
          "help": "Disable colors",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "default",
          "default": false,
          "negatable": false
        },
        {
          "name": "strict-mode",
          "help": "Strict mode",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "default",
          "default": false,
          "conflict_mode": "error",
          "negatable": true
        }
      ]
    },
    "secret": {
      "name": "secret",
      "help": "Hidden maintenance command",
      "effect": "read_only",
      "hidden": true
    },
    "shell": {
      "name": "shell",
      "help": "Interactive shell",
      "effect": "read_only",
      "interactive": true
    },
    "serve": {
      "name": "serve",
      "help": "Start the server",
      "effect": "read_only",
      "config_fields": [
        "api.key",
        "listen_port"
      ]
    }
  },
  "groups": {
    "config": {
      "name": "config",
      "help": "Manage persistent configuration values stored in the config file",
      "commands": {
        "path": {
          "name": "path",
          "help": "Print the absolute path to this application's config file and nothing else, so the value can be piped straight into another command. The path is $XDG_CONFIG_HOME/<app>/config.<toml|json> (falling back to ~/.config), or the explicit override the application was built with. Printing it does not create the file, and reports the same path whether or not one exists yet.",
          "effect": "read_only",
          "forwarding": {
            "reason": "framework-internal: absorbs app-defined global flag values"
          }
        },
        "show": {
          "name": "show",
          "help": "Show every flag and config field with its effective value and where that value came from, resolved through the precedence chain environment variable, then config file, then declared default. Declared infrastructure roots, handshake and connection environment variables are listed too. Choose --plain for an aligned human-readable table; the framework-owned --json yields the same information as a machine-readable object carrying each entry's type, default and help text.",
          "effect": "read_only",
          "payload_schema": {
            "type": "object"
          },
          "flags": [
            {
              "name": "plain",
              "help": "Display config values in a human-readable table format",
              "value_schema": {
                "type": "boolean"
              },
              "presence": "default",
              "default": false,
              "negatable": true
            }
          ],
          "forwarding": {
            "reason": "framework-internal: absorbs app-defined global flag values"
          }
        },
        "set": {
          "name": "set",
          "help": "Write a persistent value into the config file so it overrides a flag's declared default on every later run. The value is coerced to the flag's own type and rejected if it does not fit: repeatable flags take a comma-separated list (backslash-escape a literal comma) and are checked for duplicates, dict flags take a JSON object. Use --default to drop a key back to its default, and --clear to empty a repeatable flag.",
          "effect": "mutating",
          "flags": [
            {
              "name": "write",
              "help": "What to write at the key: a value, a clear, or a reset to the declared default",
              "presence": "required",
              "choices": [
                {
                  "name": "value",
                  "help": "Write a value at the key",
                  "flags": [
                    {
                      "name": "value",
                      "help": "Write this value at the key, coerced to the key's own type (comma-separated for a repeatable flag, backslash-escaping a literal comma; a JSON object for a dict flag)",
                      "value_schema": {
                        "type": "string"
                      },
                      "presence": "required"
                    }
                  ]
                },
                {
                  "name": "clear",
                  "help": "Clear a repeatable flag"
                },
                {
                  "name": "default",
                  "help": "Reset the key to its declared default"
                }
              ],
              "elect_by": "member-flags"
            }
          ],
          "args": [
            {
              "name": "key",
              "help": "The config key to set, matching a registered flag name",
              "value_schema": {
                "type": "string"
              },
              "presence": "required"
            }
          ],
          "forwarding": {
            "reason": "framework-internal: absorbs app-defined global flag values"
          }
        },
        "edit": {
          "name": "edit",
          "help": "Open this application's config file in the editor named by $EDITOR, falling back to vi. The parent directory and an empty config file are created first if they do not exist, so the editor always opens something. Launching the editor counts as a mutation: under --dry-run the command records the editor invocation and opens nothing.",
          "effect": "mutating",
          "interactive": true,
          "forwarding": {
            "reason": "framework-internal: absorbs app-defined global flag values"
          }
        },
        "init": {
          "name": "init",
          "help": "Create a starter config file listing every flag and config field the application declares, each commented with its help text, type and default value, so the file documents itself. The format follows whichever of TOML or JSON the application was built for. Refuses with an error if a config file already exists rather than overwriting it; the created path is printed on success.",
          "effect": "mutating",
          "forwarding": {
            "reason": "framework-internal: absorbs app-defined global flag values"
          }
        }
      }
    },
    "db": {
      "name": "db",
      "help": "Database operations",
      "commands": {
        "migrate": {
          "name": "migrate",
          "help": "Run migrations",
          "effect": "read_only",
          "flags": [
            {
              "name": "steps",
              "help": "Migration steps",
              "value_schema": {
                "type": "integer"
              },
              "presence": "optional"
            }
          ],
          "tags": [
            "infra"
          ],
          "config_fields": [
            "listen_port"
          ]
        },
        "seed": {
          "name": "seed",
          "help": "Seed database",
          "effect": "read_only",
          "tags": [
            "infra"
          ]
        }
      },
      "groups": {
        "cache": {
          "name": "cache",
          "help": "Cache operations",
          "commands": {
            "clear": {
              "name": "clear",
              "help": "Clear cache",
              "effect": "read_only",
              "tags": [
                "infra"
              ]
            },
            "stats": {
              "name": "stats",
              "help": "Show cache stats",
              "effect": "read_only",
              "flags": [
                {
                  "name": "detailed",
                  "help": "Show detailed stats",
                  "value_schema": {
                    "type": "boolean"
                  },
                  "presence": "required",
                  "negatable": true
                }
              ],
              "tags": [
                "infra"
              ]
            }
          }
        }
      },
      "deprecated": {
        "reset": "Use 'db migrate --steps -1' instead"
      },
      "tags": [
        "infra"
      ]
    }
  },
  "deprecated": {
    "old-cmd": "Use 'new-cmd' instead"
  },
  "tag_contracts": {
    "quality": "chatter"
  },
  "checks": {
    "db-ping": {
      "tags": [
        "infra"
      ],
      "severity": "warn",
      "fast": false,
      "pure": false,
      "needs_network": true,
      "depends_on": [
        "lint-clean"
      ],
      "scope": "db"
    },
    "lint-clean": {
      "tags": [
        "quality"
      ],
      "severity": "error",
      "fast": true,
      "pure": true,
      "needs_network": false,
      "depends_on": []
    }
  },
  "config_fields": {
    "api.key": {
      "value_schema": {
        "type": "string"
      },
      "help": "API key for the service",
      "required": true,
      "bound_commands": [
        "serve"
      ]
    },
    "listen_port": {
      "value_schema": {
        "type": "integer"
      },
      "help": "Port to listen on",
      "required": false,
      "default": 8080,
      "bound_commands": [
        "serve",
        "db migrate"
      ]
    },
    "debug": {
      "value_schema": {
        "type": "boolean"
      },
      "help": "Enable debug mode",
      "required": false,
      "default": false
    }
  },
  "infra": {
    "roots": [
      {
        "env_var": "RICH_HOME",
        "default": "/var/lib/richapp"
      }
    ],
    "handshakes": [
      {
        "env_var": "RICH_SESSION",
        "help": "Session token from the invoking process"
      }
    ]
  }
}`;

const CHECKS_TOML = `app = "richapp"

[checks.lint-clean]
tags = ["quality"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.db-ping]
tags = ["infra"]
severity = "warn"
fast = false
pure = false
needs_network = true
depends_on = ["lint-clean"]
scope = "db"
`;

/** Builds the TS rich app; registration order mirrors the Python mirror app. */
export function buildRichApp(): App {
	const app = createApp({
		name: "richapp",
		version: "2.5.0",
		help: "A comprehensive schema app",
		envPrefix: "RICH",
		config: true,
		infraRoot: { RICH_HOME: "/var/lib/richapp" },
		handshakeEnv: { RICH_SESSION: "Session token from the invoking process" },
		checksEmbed: CHECKS_TOML,
		flags: {
			chatter: flag("chatter", t.bool, {
				help: "Enable chatter output",
				short: "V",
				presence: "default",
				default: false,
			}),
			log_level: flag("log-level", t.str, {
				help: "Logging level",
				presence: "default",
				default: "info",
				env: "RICH_LOG_LEVEL",
				choices: [
					{ value: "debug" },
					{ value: "info" },
					{ value: "warn" },
					{ value: "error" },
				],
			}),
			state_file: flag("state-file", t.str, {
				help: "State file relative to the infra root",
				presence: "default",
				default: relativeToRoot("RICH_HOME", "state", "app.db"),
			}),
		},
	});

	app.configField("api.key", { type: t.str, help: "API key for the service" });
	app.configField("listen_port", {
		type: t.int,
		help: "Port to listen on",
		default: 8080n,
	});
	app.configField("debug", {
		type: t.bool,
		help: "Enable debug mode",
		default: false,
	});

	app.errorCheck("lint-clean", (_ctx, r) => r.passed("clean"));
	app.warnCheck("db-ping", (_ctx, r) => r.passed("pong"));

	app.command(
		defineReadOnlyCommand("types", {
			help: "Test all flag types",
			flags: {
				name: flag("name", t.str, {
					help: "A string flag",
					presence: "default",
					default: "world",
				}),
				count: flag("count", t.int, {
					help: "An integer flag",
					presence: "default",
					default: 42n,
				}),
				big: flag("big", t.int, {
					help: "A big integer flag",
					presence: "default",
					default: 9007199254740993n,
				}),
				ratio: flag("ratio", t.float, {
					help: "A float flag",
					presence: "default",
					default: 3.14,
				}),
				sim_run: flag("sim-run", t.bool, {
					help: "Dry run mode",
					presence: "required",
				}),
				cache_file: flag("cache-file", t.str, {
					help: "Cache file relative to the infra root",
					presence: "default",
					default: relativeToRoot("RICH_HOME", "cache.bin"),
				}),
			},
			args: [
				arg("target", t.str, {
					help: "Target to process",
					presence: "required",
				}),
			],
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("multi", {
			help: "Test list and dict flags",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "Tags to apply",
					unique: true,
					env: "RICH_TAGS",
					envSeparator: ",",
					presence: "default",
					default: [],
				}),
				port: flag("port", t.list(t.int), {
					help: "Ports to open",
					unique: false,
					presence: "default",
					default: [80n, 443n],
				}),
				// Insertion order beta-then-alpha; the schema must sort dict keys.
				matrix: flag("matrix", t.dict(t.int), {
					help: "Named weights",
					presence: "default",
					default: new Map([
						["beta", 2n],
						["alpha", 1n],
					]),
				}),
			},
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("output", {
			help: "Test a member-spelled selector",
			flags: {
				format: memberChoiceFlag(
					"format",
					{
						"as-json": choice({ help: "JSON output" }),
						yaml: choice({ help: "YAML output" }),
						text: choice({ help: "Text output" }),
					},
					{ help: "Output format", presence: "required" },
				),
			},
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("deploy", {
			help: "Test dependencies",
			flags: {
				host: flag("host", t.str, {
					help: "Deploy host",
					presence: "optional",
				}),
				port_num: flag("port-num", t.int, {
					help: "Deploy port",
					presence: "optional",
				}),
				ssl: flag("ssl", t.bool, { help: "Use SSL", presence: "required" }),
				cert: flag("cert", t.str, {
					help: "SSL certificate path",
					presence: "optional",
				}),
			},
			constraints: [
				allOrNone({
					name: "endpoint",
					members: [{ name: "host" }, { name: "port-num" }],
				}),
				requires({ name: "cert-ssl", flag: "cert", dependsOn: "ssl" }),
			],
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("notify", {
			help: "Test implies dependency",
			flags: {
				email: flag("email", t.bool, {
					help: "Send email notification",
					presence: "required",
				}),
				alert: flag("alert", t.bool, {
					help: "Enable alerts",
					presence: "required",
				}),
			},
			constraints: [
				implies({
					name: "email-alert",
					flag: "email",
					implies: "alert",
					value: true,
				}),
			],
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("query", {
			help: "Test flag sets",
			flagSets: [
				flagSet("pagination", {
					page: flag("page", t.int, {
						help: "Page number",
						presence: "default",
						default: 1n,
					}),
					per_page: flag("per-page", t.int, {
						help: "Items per page",
						presence: "default",
						default: 20n,
					}),
				}),
			],
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("files", {
			help: "Test args",
			args: [
				arg("src", t.str, { help: "Source directory", presence: "required" }),
				arg("mode", t.str, {
					help: "Copy mode",
					presence: "default",
					default: "fast",
				}),
				arg("extra", t.str, {
					help: "Extra files",
					presence: "optional",
					variadic: true,
				}),
			],
			handler: () => 0,
		}),
	);

	app.command(
		readOnlyPassthrough("exec", {
			help: "Execute a command",
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("lint", {
			help: "Run linters",
			tags: ["quality", "ci"],
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("level", {
			help: "Test int/float choices",
			flags: {
				priority: flag("priority", t.int, {
					help: "Priority level",
					choices: [
						{ value: 1n },
						{ value: 2n },
						{ value: 3n },
						{ value: 4n },
						{ value: 5n },
					],
					presence: "default",
					default: 3n,
				}),
				threshold: flag("threshold", t.float, {
					help: "Threshold value",
					choices: [{ value: 0.1 }, { value: 0.5 }, { value: 0.9 }],
					presence: "default",
					default: 0.5,
				}),
			},
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("info", {
			help: "Show info",
			flags: {
				format: flag("format", t.str, {
					help: "Output format",
					short: "f",
					presence: "default",
					default: "table",
				}),
				color_off: flag("color-off", t.bool, {
					help: "Disable colors",
					negatable: false,
					presence: "default",
					default: false,
				}),
				strict_mode: flag("strict-mode", t.bool, {
					help: "Strict mode",
					conflictMode: "error",
					presence: "default",
					default: false,
				}),
			},
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("secret", {
			help: "Hidden maintenance command",
			hidden: true,
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("shell", {
			help: "Interactive shell",
			interactive: true,
			handler: () => 0,
		}),
	);

	app.command(
		defineReadOnlyCommand("serve", {
			help: "Start the server",
			configFields: ["api.key", "listen_port"],
			handler: () => 0,
		}),
	);

	app.deprecate(deprecated("old-cmd", "Use 'new-cmd' instead"));
	app.tagContract("quality", "chatter");

	const db = app.group("db", { help: "Database operations", tags: ["infra"] });
	db.command(
		defineReadOnlyCommand("migrate", {
			help: "Run migrations",
			flags: {
				steps: flag("steps", t.int, {
					help: "Migration steps",
					presence: "optional",
				}),
			},
			configFields: ["listen_port"],
			handler: () => 0,
		}),
	);
	db.command(
		defineReadOnlyCommand("seed", { help: "Seed database", handler: () => 0 }),
	);
	db.deprecate(deprecated("reset", "Use 'db migrate --steps -1' instead"));

	const cache = db.group("cache", { help: "Cache operations" });
	cache.command(
		defineReadOnlyCommand("clear", { help: "Clear cache", handler: () => 0 }),
	);
	cache.command(
		defineReadOnlyCommand("stats", {
			help: "Show cache stats",
			flags: {
				detailed: flag("detailed", t.bool, {
					help: "Show detailed stats",
					presence: "required",
				}),
			},
			handler: () => 0,
		}),
	);

	return app;
}

function buildMinimalApp(): App {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("greet", { help: "say hello", handler: () => 0 }),
	);
	return app;
}

/** Runs fn with cwd switched to a fresh temp dir; restores cwd afterwards. */
async function withTempCwd<T>(fn: (dir: string) => Promise<T>): Promise<T> {
	const oldCwd = process.cwd();
	process.chdir(tempDir("strictcli-schema-"));
	try {
		// process.cwd() (not the tempDir result) so symlinked tmpdirs compare
		// equal to paths produced by resolve().
		return await fn(process.cwd());
	} finally {
		process.chdir(oldCwd);
	}
}

// --- Rich-app structural equality ---

test("--dump-schema writes the expected rich-app schema file", async () => {
	await withTempCwd(async (dir) => {
		writeFileSync("package.json", '{"name": "richapp"}\n');
		const res = await buildRichApp().test(["--dump-schema"]);
		assert.equal(res.stderr, "");
		assert.equal(res.exitCode, 0);
		const schemaPath = join(dir, ".strictcli", "schema.json");
		assert.equal(res.stdout, `${schemaPath}\n`);

		const raw = readFileSync(schemaPath, "utf8");
		// Exact sibling formatting: 2-space indent, ": " separator, trailing \n.
		assert.ok(raw.startsWith('{\n  "schema_version": 2,\n  "defaults": {\n'));
		assert.ok(raw.endsWith("\n"));
		assert.ok(!raw.endsWith("\n\n"));
		// BigInt defaults are bare integer tokens, precise beyond 2^53.
		assert.ok(raw.includes('"default": 9007199254740993'));
		// Float defaults are SCF tokens (valid JSON numbers).
		assert.ok(raw.includes('"default": 3.14'));

		const parsed = JSON.parse(raw) as Record<string, unknown>;
		assert.equal(parsed.project_id, "richapp");
		// project_id sits immediately after defaults (Python layout).
		assert.deepEqual(Object.keys(parsed).slice(0, 5), [
			"schema_version",
			"defaults",
			"project_id",
			"name",
			"version",
		]);
		delete parsed.project_id;
		assert.deepEqual(parsed, JSON.parse(EXPECTED_JSON));
	});
});

test("dumpSchemaDict is CWD-free, has no project_id, and matches the file content", () => {
	const dict = buildRichApp().dumpSchemaDict();
	assert.ok(!("project_id" in dict));
	// Integer schema values are bigint (BigInt int64 end-to-end).
	assert.equal(dict.schema_version, 2n);
	assert.deepEqual(JSON.parse(schemaJson(dict)), JSON.parse(EXPECTED_JSON));
});

// Contract §23.7: `default` is emitted exactly when presence is "default", and
// then ALWAYS -- including for the empty collections, which used to be the
// framework's own silent value and had nothing to announce. The Python and Go
// suites pin the same pair on their own dumps.
test("empty-collection defaults are emitted, not omitted", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "t" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "tags",
					presence: "default",
					default: [],
				}),
				header: flag("header", t.dict(t.str), {
					help: "headers",
					presence: "default",
					default: new Map(),
				}),
			},
			handler: () => 0,
		}),
	);
	const dumped = JSON.parse(schemaJson(app.dumpSchemaDict())) as {
		commands: {
			cmd: { flags: { name: string; presence: string; default: unknown }[] };
		};
	};
	const byName = (name: string) => {
		const f = dumped.commands.cmd.flags.find((e) => e.name === name);
		assert.ok(f, `flag ${name} missing from the dump`);
		return f;
	};
	assert.equal(byName("tag").presence, "default");
	assert.deepEqual(byName("tag").default, []);
	assert.equal(byName("header").presence, "default");
	assert.deepEqual(byName("header").default, {});
});

// --- Conformance case behaviors (cases/dump_schema.json) ---

test("--dump-schema exits 0 and prints the absolute schema path", async () => {
	await withTempCwd(async (dir) => {
		writeFileSync("package.json", '{"name": "myapp"}\n');
		const res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 0);
		assert.ok(res.stdout.includes(".strictcli/schema.json"));
		assert.ok(res.stdout.startsWith("/"));
		assert.equal(res.stdout, `${join(dir, ".strictcli", "schema.json")}\n`);
	});
});

// --- The declared --dump-schema location ---

test("--dump-schema writes the declared relative path, not the default", async () => {
	await withTempCwd(async (dir) => {
		writeFileSync("package.json", '{"name": "myapp"}\n');
		const app = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "test app",
			schemaPath: join("build", "cli-schema.json"),
		});
		app.command(
			defineReadOnlyCommand("greet", { help: "say hello", handler: () => 0 }),
		);
		const res = await app.test(["--dump-schema"]);
		assert.equal(res.exitCode, 0);
		const want = join(dir, "build", "cli-schema.json");
		assert.equal(res.stdout, `${want}\n`);
		assert.ok(existsSync(want));
		assert.ok(!existsSync(join(dir, ".strictcli")));
	});
});

test("--dump-schema writes a declared relativeToRoot() location", async () => {
	await withTempCwd(async (dir) => {
		writeFileSync("package.json", '{"name": "myapp"}\n');
		const root = join(dir, "root");
		const app = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "test app",
			infraRoot: { MYAPP_HOME: root },
			schemaPath: relativeToRoot("MYAPP_HOME", "schema.json"),
		});
		app.command(
			defineReadOnlyCommand("greet", { help: "say hello", handler: () => 0 }),
		);
		assert.equal((await app.test(["--dump-schema"])).exitCode, 0);
		assert.ok(existsSync(join(root, "schema.json")));
	});
});

test("the default --dump-schema location is anchored at construction", async () => {
	await withTempCwd(async (dir) => {
		writeFileSync("package.json", '{"name": "myapp"}\n');
		const app = buildMinimalApp();
		const elsewhere = tempDir("strictcli-elsewhere-");
		// project_id is read from the cwd at dump time -- a separate cwd
		// dependency this test is not about, so both directories carry one.
		writeFileSync(join(elsewhere, "package.json"), '{"name": "myapp"}\n');
		const back = process.cwd();
		process.chdir(elsewhere);
		try {
			assert.equal((await app.test(["--dump-schema"])).exitCode, 0);
		} finally {
			process.chdir(back);
		}
		assert.ok(existsSync(join(dir, ".strictcli", "schema.json")));
		assert.ok(!existsSync(join(elsewhere, ".strictcli")));
	});
});

// --- project_id-change guard on existing schema files ---

test("existing schema with a different project_id blocks the dump", async () => {
	await withTempCwd(async () => {
		writeFileSync("package.json", '{"name": "myapp"}\n');
		mkdirSync(".strictcli");
		const stale = '{"project_id": "other-proj"}\n';
		writeFileSync(join(".strictcli", "schema.json"), stale);
		const res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 1);
		assert.equal(
			res.stderr,
			"error: Schema mismatch: existing schema belongs to project " +
				"'other-proj', not 'myapp'. Run from the correct project directory.\n",
		);
		// The guard fires before the write: the stale file is untouched.
		assert.equal(
			readFileSync(join(".strictcli", "schema.json"), "utf8"),
			stale,
		);
	});
});

test("guard passes silently on unparseable, id-less, and matching existing schemas", async () => {
	await withTempCwd(async () => {
		writeFileSync("package.json", '{"name": "myapp"}\n');
		mkdirSync(".strictcli");
		const schemaPath = join(".strictcli", "schema.json");

		// Unparseable JSON: overwritten without complaint.
		writeFileSync(schemaPath, "not json{");
		let res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 0);

		// Valid JSON without project_id: overwritten without complaint.
		writeFileSync(schemaPath, '{"name": "whatever"}\n');
		res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 0);

		// Matching project_id (the file just written): re-dump succeeds.
		res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 0);
		const parsed = JSON.parse(readFileSync(schemaPath, "utf8")) as {
			project_id: string;
		};
		assert.equal(parsed.project_id, "myapp");
	});
});

// --- project_id derivation errors (package.json) ---

test("missing package.json is a hard error", async () => {
	await withTempCwd(async () => {
		const res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 1);
		assert.equal(
			res.stderr,
			"error: Cannot determine project_id: package.json not found\n",
		);
	});
});

test("unparseable package.json is a read error", async () => {
	await withTempCwd(async () => {
		writeFileSync("package.json", "{broken");
		const res = await buildMinimalApp().test(["--dump-schema"]);
		assert.equal(res.exitCode, 1);
		assert.ok(
			res.stderr.startsWith(
				"error: Cannot determine project_id: error reading package.json: ",
			),
		);
	});
});

test("package.json without a usable name field is a hard error", async () => {
	await withTempCwd(async () => {
		for (const content of ["{}", '{"name": ""}', '{"name": 42}']) {
			writeFileSync("package.json", content);
			const res = await buildMinimalApp().test(["--dump-schema"]);
			assert.equal(res.exitCode, 1);
			assert.equal(
				res.stderr,
				"error: Cannot determine project_id: no name field in package.json\n",
			);
		}
	});
});

// --- The presence key (contract §13's presence-round amendment) ---

test("schema: presence is on every flag and arg entry, always", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("src", t.str, { help: "source", presence: "required" }),
				arg("dest", t.str, { help: "destination", presence: "optional" }),
				arg("mode", t.str, {
					help: "mode",
					presence: "default",
					default: "fast",
				}),
			],
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
				host: flag("host", t.str, { help: "the host", presence: "optional" }),
				tag: flag("tag", t.list(t.str), {
					help: "tags",
					presence: "default",
					default: [],
				}),
				meta: flag("meta", t.dict(t.int), {
					help: "metadata",
					presence: "default",
					default: new Map(),
				}),
				label: flag("label", t.str, {
					help: "a label",
					presence: "default",
					default: "",
				}),
				chatter: flag("chatter", t.bool, {
					help: "chatter",
					presence: "default",
					default: false,
				}),
			},
			handler: () => 0,
		}),
	);
	const dict = app.dumpSchemaDict() as Record<string, unknown> & {
		commands: Record<string, unknown>;
	};
	const cmd = dict.commands.cmd as {
		flags: Record<string, unknown>[];
		args: Record<string, unknown>[];
	};
	const byName = new Map(cmd.flags.map((f) => [f.name as string, f]));
	assert.equal(byName.get("target")?.presence, "required");
	assert.ok(!("default" in (byName.get("target") ?? {})));
	assert.equal(byName.get("host")?.presence, "optional");
	assert.ok(!("default" in (byName.get("host") ?? {})));
	// A declared default is emitted ALWAYS, whatever the value: [], {}, "",
	// false and 0 are declarations rather than the absence of one.
	assert.deepEqual(byName.get("tag")?.default, []);
	assert.deepEqual(byName.get("meta")?.default, new Map());
	assert.equal(byName.get("label")?.default, "");
	assert.equal(byName.get("chatter")?.default, false);

	const args = new Map(cmd.args.map((a) => [a.name as string, a]));
	assert.equal(args.get("src")?.presence, "required");
	assert.equal(args.get("dest")?.presence, "optional");
	assert.equal(args.get("mode")?.presence, "default");
	assert.equal(args.get("mode")?.default, "fast");
	// The arg entry's `required` key is deleted, here and in the defaults block.
	for (const a of cmd.args) {
		assert.ok(!("required" in a));
	}
	const defaults = dict.defaults as { arg: Record<string, unknown> };
	assert.ok(!("required" in defaults.arg));
});

// ---------------------------------------------------------------------------
// Schema format v2 (contract §25)
// ---------------------------------------------------------------------------

/** The command entry of a one-command app, as the dump publishes it. */
function commandEntry(app: App, name = "cmd"): Record<string, unknown> {
	const dict = app.dumpSchemaDict() as { commands: Record<string, unknown> };
	return dict.commands[name] as Record<string, unknown>;
}

function flagEntries(app: App, name = "cmd"): Record<string, unknown>[] {
	return (commandEntry(app, name).flags ?? []) as Record<string, unknown>[];
}

function oneFlagApp(f: ReturnType<typeof flag>): App {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: { a: f },
			handler: () => 0,
		}),
	);
	return app;
}

test("v2: the fragment table's rows, and the fragment's own key order", () => {
	// Every row of §25.2's table that a TS declaration can produce, with the
	// keys in the pinned order `type`, `items`, `additionalProperties`, `enum`.
	const rows: [ReturnType<typeof flag>, unknown][] = [
		[flag("a", t.str, { help: "h", presence: "optional" }), { type: "string" }],
		[
			flag("a", t.bool, { help: "h", presence: "optional" }),
			{ type: "boolean" },
		],
		[
			flag("a", t.int, { help: "h", presence: "optional" }),
			{ type: "integer" },
		],
		[
			flag("a", t.float, { help: "h", presence: "optional" }),
			{ type: "number" },
		],
		[
			flag("a", t.list(t.str), { help: "h", presence: "optional" }),
			{ type: "array", items: { type: "string" } },
		],
		[
			flag("a", t.dict(t.float), { help: "h", presence: "optional" }),
			{ type: "object", additionalProperties: { type: "number" } },
		],
		[
			flag("a", t.str, {
				help: "h",
				presence: "optional",
				choices: [{ value: "x" }, { value: "y" }],
			}),
			{ type: "string", enum: ["x", "y"] },
		],
		[
			// An ARRAY-shaped carrier carries its enum INSIDE items, describing
			// the element -- never at the fragment root, which would say the array
			// itself must equal one of the choices.
			flag("a", t.list(t.int), {
				help: "h",
				presence: "optional",
				choices: [{ value: 1n }, { value: 2n }],
			}),
			{ type: "array", items: { type: "integer", enum: [1n, 2n] } },
		],
	];
	for (const [f, fragment] of rows) {
		const entry = flagEntries(oneFlagApp(f))[0] as Record<string, unknown>;
		assert.deepEqual(entry.value_schema, fragment);
		assert.deepEqual(
			Object.keys(entry.value_schema as object),
			Object.keys(fragment as object),
		);
		// The v1 keys are GONE: the fragment carries the value's shape, arity
		// included, so `type` and `repeatable` were two more spellings of it.
		assert.ok(!("type" in entry));
		assert.ok(!("repeatable" in entry));
	}
});

test("v2: an optional flag emits the plain type -- there is no null in a fragment", () => {
	const entry = flagEntries(
		oneFlagApp(flag("a", t.str, { help: "h", presence: "optional" })),
	)[0] as Record<string, unknown>;
	// Presence is the sole authority on absence; a nullable fragment would be a
	// second statement about the same fact.
	assert.deepEqual(entry.value_schema, { type: "string" });
	assert.equal(entry.presence, "optional");
});

test("v2: a variadic arg publishes the array fragment in either spelling", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("files", t.str, {
					help: "the files",
					presence: "optional",
					variadic: true,
					choices: [{ value: "a" }, { value: "b" }],
				}),
			],
			handler: () => 0,
		}),
	);
	const args = commandEntry(app).args as Record<string, unknown>[];
	assert.deepEqual((args[0] as Record<string, unknown>).value_schema, {
		type: "array",
		items: { type: "string", enum: ["a", "b"] },
	});
	// `variadic` SURVIVES the arity rule that deleted `repeatable`: it names a
	// token-consumption rule a consumer needs for `<files>...`.
	assert.equal((args[0] as Record<string, unknown>).variadic, true);
});

test("v2: a choices declaration splits into an enum and the sibling records", () => {
	const entry = flagEntries(
		oneFlagApp(
			flag("a", t.str, {
				help: "h",
				presence: "optional",
				choices: [
					{ value: "head", help: "the current commit only" },
					{ value: "branches" },
				],
			}),
		),
	)[0] as Record<string, unknown>;
	assert.deepEqual(entry.value_schema, {
		type: "string",
		enum: ["head", "branches"],
	});
	// `help` is omitted when the entry declares none, so an absent help and an
	// empty one cannot produce different bytes for the same declaration.
	assert.deepEqual(entry.choices, [
		{ value: "head", help: "the current commit only" },
		{ value: "branches" },
	]);
});

test("v2: the flag entry's key order is the pinned one", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				it: flag("it", t.list(t.str), {
					help: "h",
					short: "i",
					env: "MYAPP_IT",
					envSeparator: ",",
					prefixed: false,
					unique: true,
					conflictMode: "error",
					choices: [{ value: "x" }],
					presence: "default",
					default: [],
				}),
			},
			handler: () => 0,
		}),
	);
	assert.deepEqual(Object.keys(flagEntries(app)[0] as object), [
		"name",
		"help",
		"value_schema",
		"short",
		"presence",
		"default",
		"env",
		"env_separator",
		"prefixed",
		"choices",
		"unique",
		"conflict_mode",
	]);
});

test("v2: prefixed is omitted at its baseline and emitted when declared false", () => {
	const declared = flagEntries(
		oneFlagApp(
			flag("a", t.str, { help: "h", presence: "optional", prefixed: false }),
		),
	)[0] as Record<string, unknown>;
	assert.equal(declared.prefixed, false);
	const baseline = flagEntries(
		oneFlagApp(flag("a", t.str, { help: "h", presence: "optional" })),
	)[0] as Record<string, unknown>;
	assert.ok(!("prefixed" in baseline));
});

test("v2: a command's flag sets are published beside its flags", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	const pagination = flagSet("pagination", {
		page: flag("page", t.int, {
			help: "page",
			presence: "default",
			default: 1n,
		}),
	});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flagSets: [pagination],
			handler: () => 0,
		}),
	);
	const entry = commandEntry(app);
	// The member flags keep their ordinary entries, so this adds a grouping
	// without duplicating a declaration.
	assert.deepEqual(entry.flag_sets, [{ name: "pagination", flags: ["page"] }]);
	assert.equal((entry.flags as unknown[]).length, 1);
});

test("v2: the three app-level config keys appear only off their baseline", () => {
	const plain = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		config: true,
	}).dumpSchemaDict();
	assert.ok(!("config_format" in plain));
	assert.ok(!("config_path" in plain));
	assert.ok(!("config_conflict_mode" in plain));

	const declared = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		config: true,
		configFormat: "toml",
		configPath: "./etc/myapp.toml",
		configConflictMode: "error",
	}).dumpSchemaDict();
	assert.equal(declared.config_format, "toml");
	// The DECLARATION, never the resolution: the resolved absolute path is a
	// property of the dumping machine.
	assert.equal(declared.config_path, "./etc/myapp.toml");
	assert.equal(declared.config_conflict_mode, "error");
});

test("v2: a relativeToRoot config path publishes the marker, not the resolution", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		config: true,
		infraRoot: { MYAPP_HOME: "/opt/myapp" },
		configPath: relativeToRoot("MYAPP_HOME", "etc", "config.json"),
	});
	assert.deepEqual(app.dumpSchemaDict().config_path, {
		relative_to_root: { env_var: "MYAPP_HOME", parts: ["etc", "config.json"] },
	});
});

test("v2: a config field entry carries a fragment and keeps `required`", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		config: true,
	});
	app.configField("listen_port", {
		type: t.int,
		help: "the port",
		default: 8080n,
	});
	const fields = app.dumpSchemaDict().config_fields as Record<
		string,
		Record<string, unknown>
	>;
	assert.deepEqual(Object.keys(fields.listen_port as object), [
		"value_schema",
		"help",
		"required",
		"default",
	]);
	assert.deepEqual(
		(fields.listen_port as Record<string, unknown>).value_schema,
		{
			type: "integer",
		},
	);
	assert.equal((fields.listen_port as Record<string, unknown>).required, false);
});

// ---------------------------------------------------------------------------
// The byte canon (§25.8)
//
// The committed .strictcli/schema.json must be DUMPER-INDEPENDENT: a repository
// whose file is written sometimes by one implementation and sometimes by
// another must see a diff exactly when something changed.
// ---------------------------------------------------------------------------

test("canon: escaping is exactly what JSON mandates, and nothing else", () => {
	// Non-ASCII is raw UTF-8; `<`, `>` and `&` are literal; `/` is never
	// escaped. Control characters take JSON's short escapes where one exists
	// and \u00XX otherwise.
	assert.equal(schemaJson("héllo — ünïcode"), '"héllo — ünïcode"');
	assert.equal(schemaJson("<a href='x'> & </a>"), "\"<a href='x'> & </a>\"");
	assert.equal(schemaJson("a/b/c"), '"a/b/c"');
	assert.equal(
		schemaJson('quote " and backslash \\'),
		'"quote \\" and backslash \\\\"',
	);
	assert.equal(schemaJson("tab\tnewline\n"), '"tab\\tnewline\\n"');
	assert.equal(schemaJson("bell"), '"bell\\u0007"');
	// A lone surrogate is reachable only from a TS string literal, and the
	// alternative to escaping it is emitting invalid UTF-8.
	assert.equal(schemaJson("\ud800"), '"\\ud800"');
});

test("canon: numbers are SCF floats and bare integer tokens", () => {
	assert.equal(schemaJson(1n), "1");
	assert.equal(schemaJson(-9007199254740993n), "-9007199254740993");
	assert.equal(schemaJson(0.1), "0.1");
	assert.equal(schemaJson(1e-7), "1e-7");
	assert.equal(schemaJson(3), "3.0");
});

test("canon: layout is two-space indent, one member per line, inline empties", () => {
	assert.equal(
		schemaJson({ a: 1n, b: [1n, 2n], c: {}, d: [] }),
		[
			"{",
			'  "a": 1,',
			'  "b": [',
			"    1,",
			"    2",
			"  ],",
			'  "c": {},',
			'  "d": []',
			"}",
		].join("\n"),
	);
});

test("canon: the written file ends with exactly one newline", async () => {
	await withTempCwd(async (dir) => {
		writeFileSync("package.json", '{"name": "canonapp"}\n');
		await buildMinimalApp().test(["--dump-schema"]);
		const raw = readFileSync(join(dir, ".strictcli", "schema.json"), "utf8");
		assert.ok(raw.endsWith("}\n"));
		assert.ok(!raw.endsWith("\n\n"));
	});
});
