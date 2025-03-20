// Code generated by swaggo/swag. DO NOT EDIT.

package swagger

import "github.com/swaggo/swag/v2"

const docTemplate = `{
    "schemes": {{ marshal .Schemes }},"swagger":"2.0","info":{"description":"{{escape .Description}}","title":"{{.Title}}","termsOfService":"https://github.com/AlchemillaHQ/Sylve/blob/master/LICENSE","contact":{"name":"Alchemilla Ventures Pvt. Ltd.","url":"https://alchemilla.io","email":"hello@alchemilla.io"},"license":{"name":"BSD-2-Clause","url":"https://github.com/AlchemillaHQ/Sylve/blob/master/LICENSE"},"version":"{{.Version}}"},"host":"{{.Host}}","basePath":"{{.BasePath}}","paths":{"/auth/login":{"post":{"description":"Create a new JWT token","consumes":["application/json"],"produces":["application/json"],"tags":["Authentication"],"summary":"Login","parameters":[{"description":"Login request body","name":"request","in":"body","required":true,"schema":{"$ref":"#/definitions/internal_handlers.LoginRequest"}}],"responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/internal_handlers.SuccessfulLogin"}},"400":{"description":"Bad Request","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"401":{"description":"Unauthorized","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/auth/logout":{"post":{"security":[{"BearerAuth":[]}],"description":"Revoke a JWT token","consumes":["application/json"],"produces":["application/json"],"tags":["Authentication"],"summary":"Logout","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"401":{"description":"Unauthorized","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/disk/create-partitions":{"post":{"security":[{"BearerAuth":[]}],"description":"Create a partition on a disk device","consumes":["application/json"],"produces":["application/json"],"tags":["Disk"],"summary":"Create partition","parameters":[{"description":"Create partition request body","name":"request","in":"body","required":true,"schema":{"$ref":"#/definitions/internal_handlers_disk.DiskPartitionRequest"}}],"responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/disk/initialize-gpt":{"post":{"security":[{"BearerAuth":[]}],"description":"Initialize a disk with a GPT partition table","consumes":["application/json"],"produces":["application/json"],"tags":["Disk"],"summary":"Initialize GPT","parameters":[{"description":"Initialize GPT request body","name":"request","in":"body","required":true,"schema":{"$ref":"#/definitions/internal_handlers_disk.DiskActionRequest"}}],"responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/disk/list":{"get":{"security":[{"BearerAuth":[]}],"description":"List all disk devices on the system","consumes":["application/json"],"produces":["application/json"],"tags":["Disk"],"summary":"List disk devices","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-array_sylve_internal_interfaces_services_disk_Disk"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/disk/wipe":{"post":{"security":[{"BearerAuth":[]}],"description":"Wipe the partition table of a disk device","consumes":["application/json"],"produces":["application/json"],"tags":["Disk"],"summary":"Wipe disk","parameters":[{"description":"Wipe disk request body","name":"request","in":"body","required":true,"schema":{"$ref":"#/definitions/internal_handlers_disk.DiskActionRequest"}}],"responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/health/basic":{"get":{"security":[{"BearerAuth":[]}],"description":"Overall basic health check of the system","consumes":["application/json"],"produces":["application/json"],"tags":["Health"],"summary":"Basic health check","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/audit-logs":{"get":{"security":[{"BearerAuth":[]}],"description":"Get the latest audit logs","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Get Audit Logs","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-array_sylve_internal_db_models_info_AuditLog"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/basic":{"get":{"security":[{"BearerAuth":[]}],"description":"Get the basic information about the system","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Get Basic Info","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-sylve_internal_interfaces_services_info_BasicInfo"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/cpu":{"get":{"description":"Retrieves real-time CPU info","consumes":["application/json"],"produces":["application/json"],"tags":["system"],"summary":"Get Current CPU information","responses":{"200":{"description":"OK","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-sylve_internal_interfaces_services_info_CPUInfo"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/cpu/historical":{"get":{"description":"Retrieves historical CPU info","consumes":["application/json"],"produces":["application/json"],"tags":["system"],"summary":"Get Historical CPU information","responses":{"200":{"description":"OK","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-array_sylve_internal_db_models_info_CPU"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/notes":{"get":{"security":[{"BearerAuth":[]}],"description":"Get all notes stored in the database","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Get All Notes","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-array_sylve_internal_db_models_info_Note"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}},"post":{"security":[{"BearerAuth":[]}],"description":"Add a new note to the database","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Create a new note","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-sylve_internal_db_models_info_Note"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/notes/:id":{"put":{"security":[{"BearerAuth":[]}],"description":"Update a note in the database by its ID","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Update a note by ID","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"400":{"description":"Invalid note ID","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}},"delete":{"security":[{"BearerAuth":[]}],"description":"Delete a note from the database by its ID","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Delete a note by ID","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"400":{"description":"Invalid note ID","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"404":{"description":"Note not found","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/ram":{"get":{"security":[{"BearerAuth":[]}],"description":"Get the RAM information about the system","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Get RAM Info","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-sylve_internal_interfaces_services_info_RAMInfo"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/info/swap":{"get":{"security":[{"BearerAuth":[]}],"description":"Get the Swap information about the system","consumes":["application/json"],"produces":["application/json"],"tags":["Info"],"summary":"Get Swap Info","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-sylve_internal_interfaces_services_info_SwapInfo"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/zfs/avg-io-delay":{"get":{"security":[{"BearerAuth":[]}],"description":"Get the average IO delay of all pools","consumes":["application/json"],"produces":["application/json"],"tags":["ZFS"],"summary":"Get Average IO Delay","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-internal_handlers_zfs_AvgIODelayResponse"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/zfs/avg-io-delay-historical":{"get":{"security":[{"BearerAuth":[]}],"description":"Get the historical IO delays of all pools","consumes":["application/json"],"produces":["application/json"],"tags":["ZFS"],"summary":"Get Average IO Delay Historical","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-array_sylve_internal_db_models_info_IODelay"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}},"/zfs/pools":{"get":{"security":[{"BearerAuth":[]}],"description":"Get all ZFS pools","consumes":["application/json"],"produces":["application/json"],"tags":["ZFS"],"summary":"Get Pools","responses":{"200":{"description":"Success","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-array_sylve_internal_interfaces_services_zfs_Zpool"}},"500":{"description":"Internal Server Error","schema":{"$ref":"#/definitions/sylve_internal.APIResponse-any"}}}}}},"definitions":{"internal_handlers.LoginRequest":{"type":"object","required":["password","username"],"properties":{"authType":{"type":"string"},"password":{"type":"string","maxLength":128,"minLength":3},"remember":{"type":"boolean"},"username":{"type":"string","maxLength":128,"minLength":3}}},"internal_handlers.SuccessfulLogin":{"type":"object","properties":{"hostname":{"type":"string"},"token":{"type":"string"}}},"internal_handlers_disk.DiskActionRequest":{"type":"object","required":["device"],"properties":{"device":{"type":"string","minLength":2}}},"internal_handlers_disk.DiskPartitionRequest":{"type":"object","required":["device","sizes"],"properties":{"device":{"type":"string","minLength":2},"sizes":{"type":"array","items":{"type":"integer"}}}},"internal_handlers_zfs.AvgIODelayResponse":{"type":"object","properties":{"delay":{"type":"number"}}},"sylve_internal.APIResponse-any":{"type":"object","properties":{"data":{},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-array_sylve_internal_db_models_info_AuditLog":{"type":"object","properties":{"data":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_db_models_info.AuditLog"}},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-array_sylve_internal_db_models_info_CPU":{"type":"object","properties":{"data":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_db_models_info.CPU"}},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-array_sylve_internal_db_models_info_IODelay":{"type":"object","properties":{"data":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_db_models_info.IODelay"}},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-array_sylve_internal_db_models_info_Note":{"type":"object","properties":{"data":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_db_models_info.Note"}},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-array_sylve_internal_interfaces_services_disk_Disk":{"type":"object","properties":{"data":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_interfaces_services_disk.Disk"}},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-array_sylve_internal_interfaces_services_zfs_Zpool":{"type":"object","properties":{"data":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_interfaces_services_zfs.Zpool"}},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-internal_handlers_zfs_AvgIODelayResponse":{"type":"object","properties":{"data":{"$ref":"#/definitions/internal_handlers_zfs.AvgIODelayResponse"},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-sylve_internal_db_models_info_Note":{"type":"object","properties":{"data":{"$ref":"#/definitions/sylve_internal_db_models_info.Note"},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-sylve_internal_interfaces_services_info_BasicInfo":{"type":"object","properties":{"data":{"$ref":"#/definitions/sylve_internal_interfaces_services_info.BasicInfo"},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-sylve_internal_interfaces_services_info_CPUInfo":{"type":"object","properties":{"data":{"$ref":"#/definitions/sylve_internal_interfaces_services_info.CPUInfo"},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-sylve_internal_interfaces_services_info_RAMInfo":{"type":"object","properties":{"data":{"$ref":"#/definitions/sylve_internal_interfaces_services_info.RAMInfo"},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal.APIResponse-sylve_internal_interfaces_services_info_SwapInfo":{"type":"object","properties":{"data":{"$ref":"#/definitions/sylve_internal_interfaces_services_info.SwapInfo"},"error":{"type":"string"},"message":{"type":"string"},"status":{"type":"string"}}},"sylve_internal_db_models_info.AuditLog":{"type":"object","properties":{"action":{"type":"string"},"authType":{"type":"string"},"createdAt":{"type":"string"},"ended":{"type":"string"},"id":{"type":"integer"},"node":{"type":"string"},"started":{"type":"string"},"status":{"type":"string"},"updatedAt":{"type":"string"},"user":{"type":"string"},"userId":{"type":"integer"}}},"sylve_internal_db_models_info.CPU":{"type":"object","properties":{"createdAt":{"type":"string"},"id":{"type":"integer"},"usage":{"type":"number"}}},"sylve_internal_db_models_info.IODelay":{"type":"object","properties":{"createdAt":{"type":"string"},"delay":{"type":"number"},"id":{"type":"integer"}}},"sylve_internal_db_models_info.Note":{"type":"object","properties":{"content":{"type":"string"},"createdAt":{"type":"string"},"id":{"type":"integer"},"title":{"type":"string"},"updateAt":{"type":"string"}}},"sylve_internal_interfaces_services_disk.Disk":{"type":"object","properties":{"device":{"type":"string"},"gpt":{"type":"boolean"},"model":{"type":"string"},"partitions":{"type":"array","items":{"$ref":"#/definitions/sylve_internal_interfaces_services_disk.Partition"}},"serial":{"type":"string"},"size":{"type":"integer"},"smartData":{},"type":{"type":"string"},"usage":{"type":"string"},"wearOut":{"type":"string"}}},"sylve_internal_interfaces_services_disk.Partition":{"type":"object","properties":{"name":{"type":"string"},"size":{"type":"integer"},"usage":{"type":"string"}}},"sylve_internal_interfaces_services_info.BasicInfo":{"type":"object","properties":{"bootMode":{"type":"string"},"hostname":{"type":"string"},"loadAverage":{"type":"string"},"os":{"type":"string"},"sylveVersion":{"type":"string"},"uptime":{"type":"integer"}}},"sylve_internal_interfaces_services_info.CPUInfo":{"type":"object","properties":{"cache":{"type":"object","properties":{"l1d":{"type":"integer"},"l1i":{"type":"integer"},"l2":{"type":"integer"},"l3":{"type":"integer"}}},"cacheLine":{"type":"integer"},"family":{"type":"integer"},"features":{"type":"array","items":{"type":"string"}},"frequency":{"type":"integer"},"logicalCores":{"type":"integer"},"model":{"type":"integer"},"name":{"type":"string"},"physicalCores":{"type":"integer"},"threadsPerCore":{"type":"integer"},"usage":{"type":"number"}}},"sylve_internal_interfaces_services_info.RAMInfo":{"type":"object","properties":{"free":{"type":"integer"},"total":{"type":"integer"},"usedPercent":{"type":"number"}}},"sylve_internal_interfaces_services_info.SwapInfo":{"type":"object","properties":{"free":{"type":"integer"},"total":{"type":"integer"},"usedPercent":{"type":"number"}}},"sylve_internal_interfaces_services_zfs.Zpool":{"type":"object","properties":{"allocated":{"type":"integer"},"dedupRatio":{"type":"number"},"free":{"type":"integer"},"freeing":{"type":"integer"},"health":{"type":"string"},"leaked":{"type":"integer"},"name":{"type":"string"},"readOnly":{"type":"boolean"},"size":{"type":"integer"}}}},"securityDefinitions":{"BearerAuth":{"description":"Type \"Bearer\" followed by a space and JWT token.","type":"apiKey","name":"Authorization","in":"header"}}}`

// SwaggerInfo holds exported Swagger Info so clients can modify it
var SwaggerInfo = &swag.Spec{
	Version:          "0.0.1",
	Host:             "sylve.lan:8181",
	BasePath:         "/api",
	Schemes:          []string{},
	Title:            "Sylve API",
	Description:      "Sylve is a lightweight GUI for managing Bhyve, Jails, ZFS, networking, and more on FreeBSD.",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  docTemplate,
	LeftDelim:        "{{",
	RightDelim:       "}}",
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
