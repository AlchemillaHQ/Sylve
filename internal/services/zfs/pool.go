// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2025 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package zfs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	infoModels "github.com/alchemillahq/sylve/internal/db/models/info"
	zfsServiceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services/zfs"
	"github.com/alchemillahq/sylve/pkg/disk"
	"github.com/alchemillahq/sylve/pkg/zfs"
)

func (s *Service) CreatePool(ctx context.Context, req zfsServiceInterfaces.CreateZPoolRequest) error {
	if !zfs.IsValidPoolName(req.Name) {
		return fmt.Errorf("invalid_pool_name")
	}

	names, err := s.GZFS.Zpool.GetPoolNames(ctx)
	if err != nil {
		return fmt.Errorf("failed_to_get_existing_pools: %v", err)
	}

	for _, existingName := range names {
		if strings.EqualFold(existingName, req.Name) {
			return fmt.Errorf("pool_name_taken")
		}
	}

	if req.RaidType != "" {
		validRaidTypes := map[zfsServiceInterfaces.RaidType]int{
			"mirror": 2,
			"raidz":  3,
			"raidz2": 4,
			"raidz3": 5,
		}

		minDevices, ok := validRaidTypes[req.RaidType]
		if !ok {
			return fmt.Errorf("invalid_raidz_type")
		}

		for _, vdev := range req.Vdevs {
			if len(vdev.VdevDevices) < minDevices {
				return fmt.Errorf("vdev %s has insufficient devices for %s (minimum %d)", vdev.Name, req.RaidType, minDevices)
			}
		}
	} else {
		for _, vdev := range req.Vdevs {
			if len(vdev.VdevDevices) == 0 {
				return fmt.Errorf("vdev %s has no devices", vdev.Name)
			}
		}
	}

	var vdevArgs []string
	for _, vdev := range req.Vdevs {
		if req.RaidType != "" {
			vdevArgs = append(vdevArgs, string(req.RaidType))
		}
		vdevArgs = append(vdevArgs, vdev.VdevDevices...)
	}
	var args []string

	args = append(args, vdevArgs...)

	if len(req.Spares) > 0 {
		args = append(args, "spare")
		args = append(args, req.Spares...)
	}

	err = s.GZFS.Zpool.Create(ctx, req.Name, req.CreateForce, req.Properties, args...)
	if err != nil {
		return fmt.Errorf("zpool_create_failed: %v", err)
	}

	if err := s.Libvirt.CreateStoragePool(req.Name); err != nil {
		return fmt.Errorf("libvirt_create_pool_failed: %v", err)
	}

	return s.SyncToLibvirt()
}

func (s *Service) DeletePool(guid string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := zfs.GetZpoolByGUID(guid)

	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	datasets, err := pool.Datasets()
	if err != nil {
		return fmt.Errorf("failed_to_get_datasets: %v", err)
	}

	if len(datasets) > 0 {
		for _, ds := range datasets {
			inUse := s.IsDatasetInUse(ds.GUID, true)

			if inUse {
				return fmt.Errorf("dataset %s is in use and cannot be deleted", ds.Name)
			}
		}
	}

	err = pool.Destroy()

	if err != nil {
		return err
	}

	result := s.DB.Where("json_extract(pools, '$.guid') = ?", guid).
		Delete(&infoModels.ZPoolHistorical{})

	if result.Error != nil {
		return fmt.Errorf("failed_to_delete_historical_data: %v", result.Error)
	}

	if err := s.Libvirt.DeleteStoragePool(pool.Name); err != nil {
		if !strings.Contains(err.Error(), "failed to lookup storage pool") &&
			!strings.Contains(err.Error(), "Storage pool not found") {
			return err
		}
	}

	return s.SyncToLibvirt()
}

func (s *Service) ReplaceDevice(guid, old, latest string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := zfs.GetZpoolByGUID(guid)
	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	if err := pool.Replace(old, latest); err != nil {
		return fmt.Errorf("failed_to_replace_device %s: %v", old, err)
	}

	pool, err = zfs.GetZpoolByGUID(guid)
	if err != nil {
		return fmt.Errorf("pool_not_found_after_replace")
	}

	return nil
}

func (s *Service) EditPool(name string, props map[string]string, spares []string) error {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()

	pool, err := zfs.GetZpool(name)
	if err != nil {
		return fmt.Errorf("pool_not_found")
	}

	minSize := pool.RequiredSpareSize()

	for _, dev := range spares {
		sz, err := disk.GetDiskSize(dev)
		if err != nil {
			return fmt.Errorf("invalid_spare_device %s: %v", dev, err)
		}

		if sz == 0 {
			return fmt.Errorf("invalid_spare_device %s: size is zero", dev)
		}

		if sz < minSize {
			return fmt.Errorf("spare_device %s is too small, minimum size is %d bytes", dev, minSize)
		}
	}

	for prop, val := range props {
		if err := zfs.SetZpoolProperty(name, prop, val); err != nil {
			return fmt.Errorf("failed_to_set_property %s: %v", prop, err)
		}
	}

	currentSet := make(map[string]string)
	/*
			loki :: ~ » zpool list -v -j tanky | jq
		{
		  "output_version": {
		    "command": "zpool list",
		    "vers_major": 0,
		    "vers_minor": 1
		  },
		  "pools": {
		    "tanky": {
		      "name": "tanky",
		      "type": "POOL",
		      "state": "ONLINE",
		      "pool_guid": "18149119554419341117",
		      "txg": "262456",
		      "spa_version": "5000",
		      "zpl_version": "5",
		      "properties": {
		        "size": {
		          "value": "14G",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "allocated": {
		          "value": "8.31G",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "free": {
		          "value": "5.69G",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "checkpoint": {
		          "value": "-",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "expandsize": {
		          "value": "-",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "fragmentation": {
		          "value": "1%",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "capacity": {
		          "value": "59%",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "dedupratio": {
		          "value": "1.00x",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "health": {
		          "value": "ONLINE",
		          "source": {
		            "type": "NONE",
		            "data": "-"
		          }
		        },
		        "altroot": {
		          "value": "-",
		          "source": {
		            "type": "DEFAULT",
		            "data": "-"
		          }
		        }
		      },
		      "vdevs": {
		        "mirror-0": {
		          "name": "mirror-0",
		          "vdev_type": "mirror",
		          "guid": "16509626027325813777",
		          "class": "normal",
		          "state": "ONLINE",
		          "properties": {
		            "size": {
		              "value": "7G",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "allocated": {
		              "value": "4.15G",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "free": {
		              "value": "2.85G",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "checkpoint": {
		              "value": "-",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "expandsize": {
		              "value": "-",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "fragmentation": {
		              "value": "2%",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "capacity": {
		              "value": "59.3%",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "dedupratio": {
		              "value": "-",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "health": {
		              "value": "ONLINE",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            }
		          },
		          "vdevs": {
		            "ada0p8": {
		              "name": "ada0p8",
		              "vdev_type": "disk",
		              "guid": "14008906597308561430",
		              "path": "/dev/ada0p8",
		              "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p8",
		              "class": "normal",
		              "state": "ONLINE",
		              "properties": {
		                "size": {
		                  "value": "8G",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "allocated": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "free": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "checkpoint": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "expandsize": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "fragmentation": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "capacity": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "dedupratio": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "health": {
		                  "value": "ONLINE",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                }
		              }
		            },
		            "ada0p3": {
		              "name": "ada0p3",
		              "vdev_type": "disk",
		              "guid": "3297683577195417079",
		              "path": "/dev/ada0p3",
		              "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p3",
		              "class": "normal",
		              "state": "ONLINE",
		              "properties": {
		                "size": {
		                  "value": "7.45G",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "allocated": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "free": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "checkpoint": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "expandsize": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "fragmentation": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "capacity": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "dedupratio": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "health": {
		                  "value": "ONLINE",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                }
		              }
		            }
		          }
		        },
		        "mirror-1": {
		          "name": "mirror-1",
		          "vdev_type": "mirror",
		          "guid": "14166564377107343367",
		          "class": "normal",
		          "state": "ONLINE",
		          "properties": {
		            "size": {
		              "value": "7G",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "allocated": {
		              "value": "4.16G",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "free": {
		              "value": "2.84G",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "checkpoint": {
		              "value": "-",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "expandsize": {
		              "value": "-",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "fragmentation": {
		              "value": "1%",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "capacity": {
		              "value": "59.4%",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "dedupratio": {
		              "value": "-",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            },
		            "health": {
		              "value": "ONLINE",
		              "source": {
		                "type": "NONE",
		                "data": "-"
		              }
		            }
		          },
		          "vdevs": {
		            "ada0p2": {
		              "name": "ada0p2",
		              "vdev_type": "disk",
		              "guid": "16751901842275378232",
		              "path": "/dev/ada0p2",
		              "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p2",
		              "class": "normal",
		              "state": "ONLINE",
		              "properties": {
		                "size": {
		                  "value": "7.45G",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "allocated": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "free": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "checkpoint": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "expandsize": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "fragmentation": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "capacity": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "dedupratio": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "health": {
		                  "value": "ONLINE",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                }
		              }
		            },
		            "ada0p1": {
		              "name": "ada0p1",
		              "vdev_type": "disk",
		              "guid": "10240971621525487756",
		              "path": "/dev/ada0p1",
		              "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p1",
		              "class": "normal",
		              "state": "ONLINE",
		              "properties": {
		                "size": {
		                  "value": "7.45G",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "allocated": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "free": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "checkpoint": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "expandsize": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "fragmentation": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "capacity": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "dedupratio": {
		                  "value": "-",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                },
		                "health": {
		                  "value": "ONLINE",
		                  "source": {
		                    "type": "NONE",
		                    "data": "-"
		                  }
		                }
		              }
		            }
		          }
		        },
		        "logs": {
		          "ada0p5": {
		            "name": "ada0p5",
		            "vdev_type": "disk",
		            "guid": "18444579332753648538",
		            "path": "/dev/ada0p5",
		            "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p5",
		            "class": "log",
		            "state": "ONLINE",
		            "properties": {
		              "size": {
		                "value": "8G",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "allocated": {
		                "value": "0",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "free": {
		                "value": "7.50G",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "checkpoint": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "expandsize": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "fragmentation": {
		                "value": "0%",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "capacity": {
		                "value": "0.00%",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "dedupratio": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "health": {
		                "value": "ONLINE",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              }
		            }
		          }
		        },
		        "l2cache": {
		          "ada0p6": {
		            "name": "ada0p6",
		            "vdev_type": "disk",
		            "guid": "6747304902614089988",
		            "path": "/dev/ada0p6",
		            "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p6",
		            "class": "l2cache",
		            "state": "ONLINE",
		            "properties": {
		              "size": {
		                "value": "8G",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "allocated": {
		                "value": "316K",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "free": {
		                "value": "8.00G",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "checkpoint": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "expandsize": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "fragmentation": {
		                "value": "0%",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "capacity": {
		                "value": "0.00%",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "dedupratio": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "health": {
		                "value": "ONLINE",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              }
		            }
		          }
		        },
		        "spares": {
		          "ada0p7": {
		            "name": "ada0p7",
		            "vdev_type": "disk",
		            "guid": "4705887614251637164",
		            "path": "/dev/ada0p7",
		            "phys_path": "id1,enc@n3061686369656d30/type@0/slot@2/elmdesc@Slot_01/p7",
		            "class": "spare",
		            "state": "ONLINE",
		            "properties": {
		              "size": {
		                "value": "8G",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "allocated": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "free": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "checkpoint": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "expandsize": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "fragmentation": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "capacity": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "dedupratio": {
		                "value": "-",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              },
		              "health": {
		                "value": "AVAIL",
		                "source": {
		                  "type": "NONE",
		                  "data": "-"
		                }
		              }
		            }
		          }
		        }
		      }
		    }
		  }
		}
		loki :: ~ »

	*/

	// for _, dev := range pool.Spares {
	// 	base := filepath.Base(dev.Name)
	// 	if _, seen := currentSet[base]; !seen {
	// 		currentSet[base] = dev.Name
	// 	}
	// }

	for _, dev := range pool.Vdevs["spares"].Vdevs {
		base := filepath.Base(dev.Path)
		if _, seen := currentSet[base]; !seen {
			currentSet[base] = dev.Path
		}
	}

	newSet := make(map[string]struct{})
	for _, dev := range spares {
		newSet[filepath.Base(dev)] = struct{}{}
	}

	removed := make(map[string]struct{})
	for base, full := range currentSet {
		if _, keep := newSet[base]; !keep {
			if _, done := removed[base]; done {
				continue
			}
			if err := pool.RemoveSpare(full); err != nil {
				return fmt.Errorf("failed_to_remove_spare %s: %v", full, err)
			}
			removed[base] = struct{}{}
			time.Sleep(100 * time.Millisecond)
		}
	}

	time.Sleep(500 * time.Millisecond)

	for _, dev := range spares {
		base := filepath.Base(dev)
		if _, exists := currentSet[base]; !exists {
			if err := pool.AddSpare(dev); err != nil {
				return fmt.Errorf("failed_to_add_spare %s: %v", dev, err)
			}
		}
	}

	return s.SyncToLibvirt()
}

func (s *Service) SyncToLibvirt() error {
	defer s.Libvirt.RescanStoragePools()

	sPools, err := s.Libvirt.ListStoragePools()
	if err != nil {
		return fmt.Errorf("failed_to_list_libvirt_pools: %v", err)
	}

	existing := make(map[string]struct{}, len(sPools))
	for _, sp := range sPools {
		existing[sp.Name] = struct{}{}
	}

	for _, sp := range sPools {
		if _, err := zfs.GetZpool(sp.Name); err != nil {
			if derr := s.Libvirt.DeleteStoragePool(sp.Name); derr != nil {
				return fmt.Errorf("failed_to_delete_libvirt_pool %s: %v", sp.Name, derr)
			}
		}
	}

	zPools, err := zfs.ListZpools()
	if err != nil {
		return fmt.Errorf("failed_to_list_zfs_pools: %v", err)
	}

	for _, zp := range zPools {
		if _, ok := existing[zp.Name]; ok {
			continue
		}
		if err := s.Libvirt.CreateStoragePool(zp.Name); err != nil {
			return fmt.Errorf("failed_to_create_libvirt_pool %s: %w", zp.Name, err)
		}
	}

	return nil
}

func (s *Service) GetZpoolHistoricalStats(intervalMinutes int, limit int) (map[string][]zfsServiceInterfaces.PoolStatPoint, int, error) {
	// if intervalMinutes <= 0 {
	// 	return nil, 0, fmt.Errorf("invalid interval: must be > 0")
	// }

	// var records []infoModels.ZPoolHistorical
	// if err := s.DB.
	// 	Order("created_at ASC").
	// 	Find(&records).Error; err != nil {
	// 	return nil, 0, err
	// }

	// count := len(records)
	// intervalMs := int64(intervalMinutes) * 60 * 1000

	// buckets := make(map[string]map[int64]zfsServiceInterfaces.PoolStatPoint)
	// for _, rec := range records {
	// 	bucketTime := (rec.CreatedAt / intervalMs) * intervalMs
	// 	name := zfs.Zpool(rec.Pools).Name

	// 	if buckets[name] == nil {
	// 		buckets[name] = make(map[int64]zfsServiceInterfaces.PoolStatPoint)
	// 	}

	// 	if _, seen := buckets[name][bucketTime]; !seen {
	// 		p := zfs.Zpool(rec.Pools)
	// 		buckets[name][bucketTime] = zfsServiceInterfaces.PoolStatPoint{
	// 			Time:       bucketTime,
	// 			Allocated:  p.Allocated,
	// 			Free:       p.Free,
	// 			Size:       p.Size,
	// 			DedupRatio: p.DedupRatio,
	// 		}
	// 	}
	// }

	// result := make(map[string][]zfsServiceInterfaces.PoolStatPoint, len(buckets))
	// for name, mp := range buckets {
	// 	pts := make([]zfsServiceInterfaces.PoolStatPoint, 0, len(mp))
	// 	for _, pt := range mp {
	// 		pts = append(pts, pt)
	// 	}
	// 	sort.Slice(pts, func(i, j int) bool {
	// 		return pts[i].Time < pts[j].Time
	// 	})

	// 	if limit > 0 && len(pts) > limit {
	// 		pts = pts[len(pts)-limit:]
	// 	}

	// 	result[name] = pts
	// }

	// return result, count, nil

	return nil, 0, fmt.Errorf("zpool_historical_stats_not_implemented")
}
