package smart

type AttrDef struct {
	Name       string
	Format     string
	HDDOnly    bool
	SSDOnly    bool
	NoNormVal  bool
	NoWorstVal bool
}

type DriveModelEntry struct {
	ModelPattern   string
	FirmwareRegex  string
	AttrOverrides  map[uint32]AttrDef
	HDDOnly        bool
	SSDOnly        bool
}

var modelDB = []DriveModelEntry{
	{
		ModelPattern: "^Samsung SSD",
		SSDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			177: {Name: "Wear_Leveling_Count", Format: "raw48", SSDOnly: true},
			179: {Name: "Used_Rsvd_Blk_Cnt_Tot", Format: "raw48", SSDOnly: true},
			235: {Name: "POR_Recovery_Count", Format: "raw48", SSDOnly: true},
			241: {Name: "Total_LBAs_Written", Format: "raw48"},
		},
	},
	{
		ModelPattern: "^SAMSUNG MZ",
		SSDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			177: {Name: "Wear_Leveling_Count", Format: "raw48", SSDOnly: true},
			179: {Name: "Used_Rsvd_Blk_Cnt_Tot", Format: "raw48", SSDOnly: true},
			235: {Name: "POR_Recovery_Count", Format: "raw48", SSDOnly: true},
			241: {Name: "Total_LBAs_Written", Format: "raw48"},
		},
	},
	{
		ModelPattern: "^INTEL SSD",
		SSDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			175: {Name: "Program_Fail_Count_Chip", Format: "raw48", SSDOnly: true},
			176: {Name: "Erase_Fail_Count_Chip", Format: "raw48", SSDOnly: true},
			225: {Name: "Host_Writes_32MiB", Format: "raw48", SSDOnly: true},
			226: {Name: "Timed_Workload_Media_Wear", Format: "raw48", SSDOnly: true},
			227: {Name: "Timed_Workload_Host_RW", Format: "raw48", SSDOnly: true},
			228: {Name: "Timed_Workload_Timer", Format: "raw48", SSDOnly: true},
		},
	},
	{
		ModelPattern: "^Crucial_?CT",
		SSDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			202: {Name: "Percent_Lifetime_Remain", Format: "raw48", SSDOnly: true},
			246: {Name: "Total_LBAs_Written", Format: "raw48"},
		},
	},
	{
		ModelPattern: "^WDC WD",
		HDDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			22:  {Name: "Helium_Level", Format: "raw16", HDDOnly: true},
			200: {Name: "Multi_Zone_Error_Rate", Format: "raw48", HDDOnly: true},
		},
	},
	{
		ModelPattern: "^ST[0-9]",
		HDDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			1:   {Name: "Raw_Read_Error_Rate", Format: "raw48"},
			7:   {Name: "Seek_Error_Rate", Format: "raw48", HDDOnly: true},
			200: {Name: "Pressure_Limit", Format: "raw48", HDDOnly: true},
		},
	},
	{
		ModelPattern: "^TOSHIBA",
		AttrOverrides: map[uint32]AttrDef{
			23: {Name: "Helium_Condition_Lower", Format: "raw48", HDDOnly: true},
			24: {Name: "Helium_Condition_Upper", Format: "raw48", HDDOnly: true},
		},
	},
	{
		ModelPattern: "^HGST",
		HDDOnly: true,
		AttrOverrides: map[uint32]AttrDef{
			22: {Name: "Helium_Level", Format: "raw16", HDDOnly: true},
		},
	},
	{
		ModelPattern: "^Micron_",
		SSDOnly: true,
		AttrOverrides: map[uint32]AttrDef{
			202: {Name: "Percent_Lifetime_Remain", Format: "raw48", SSDOnly: true},
			246: {Name: "Total_LBAs_Written", Format: "raw48"},
		},
	},
	{
		ModelPattern: "^KINGSTON",
		SSDOnly: true,
		AttrOverrides: map[uint32]AttrDef{
			231: {Name: "SSD_Life_Left", Format: "raw48", SSDOnly: true},
			233: {Name: "Media_Wearout_Indicator", Format: "raw48", SSDOnly: true},
		},
	},
	{
		ModelPattern: "^CONSISTENT",
		SSDOnly:      true,
		AttrOverrides: map[uint32]AttrDef{
			160: {Name: "Uncorrectable_Error_Cnt", Format: "raw48", SSDOnly: true},
			161: {Name: "Valid_Spare_Block", Format: "raw48", SSDOnly: true},
			163: {Name: "Initial_Bad_Block_Count", Format: "raw48", SSDOnly: true},
			164: {Name: "Total_Erase_Count", Format: "raw48", SSDOnly: true},
			165: {Name: "Max_Erase_Count", Format: "raw48", SSDOnly: true},
			166: {Name: "Min_Erase_Count", Format: "raw48", SSDOnly: true},
			167: {Name: "Average_Erase_Count", Format: "raw48", SSDOnly: true},
			168: {Name: "Max_Erase_Count_Spec", Format: "raw48", SSDOnly: true},
			169: {Name: "Remaining_Lifetime_Perc", Format: "raw48", SSDOnly: true},
			245: {Name: "Unknown_SSD_Attribute", Format: "raw48"},
		},
	},
}

var DefaultAttrDefs = map[uint32]AttrDef{
	1:   {Name: "Raw_Read_Error_Rate", Format: "raw48"},
	2:   {Name: "Throughput_Performance", Format: "raw48"},
	3:   {Name: "Spin_Up_Time", Format: "raw16"},
	4:   {Name: "Start_Stop_Count", Format: "raw48"},
	5:   {Name: "Reallocated_Sector_Ct", Format: "raw16"},
	6:   {Name: "Read_Channel_Margin", Format: "raw48", HDDOnly: true},
	7:   {Name: "Seek_Error_Rate", Format: "raw48", HDDOnly: true},
	8:   {Name: "Seek_Time_Performance", Format: "raw48", HDDOnly: true},
	9:   {Name: "Power_On_Hours", Format: "raw24"},
	10:  {Name: "Spin_Retry_Count", Format: "raw48", HDDOnly: true},
	11:  {Name: "Calibration_Retry_Count", Format: "raw48", HDDOnly: true},
	12:  {Name: "Power_Cycle_Count", Format: "raw48"},
	13:  {Name: "Read_Soft_Error_Rate", Format: "raw48"},
	22:  {Name: "Helium_Level", Format: "raw16", HDDOnly: true},
	23:  {Name: "Helium_Condition_Lower", Format: "raw48", HDDOnly: true},
	24:  {Name: "Helium_Condition_Upper", Format: "raw48", HDDOnly: true},
	170: {Name: "Available_Reservd_Space", Format: "raw48", SSDOnly: true},
	171: {Name: "Program_Fail_Count_Chip", Format: "raw48", SSDOnly: true},
	172: {Name: "Erase_Fail_Count_Chip", Format: "raw48", SSDOnly: true},
	173: {Name: "Wear_Leveling_Count", Format: "raw48", SSDOnly: true},
	174: {Name: "Unexpected_Power_Loss", Format: "raw48", SSDOnly: true},
	175: {Name: "Program_Fail_Count_Chip", Format: "raw48", SSDOnly: true},
	176: {Name: "Erase_Fail_Count_Chip", Format: "raw48", SSDOnly: true},
	177: {Name: "Wear_Leveling_Count", Format: "raw48", SSDOnly: true},
	178: {Name: "Used_Rsvd_Blk_Cnt_Chip", Format: "raw48", SSDOnly: true},
	179: {Name: "Used_Rsvd_Blk_Cnt_Tot", Format: "raw48", SSDOnly: true},
	180: {Name: "Unused_Rsvd_Blk_Cnt_Tot", Format: "raw48", SSDOnly: true},
	181: {Name: "Program_Fail_Cnt_Total", Format: "raw48"},
	182: {Name: "Erase_Fail_Count_Total", Format: "raw48", SSDOnly: true},
	183: {Name: "Runtime_Bad_Block", Format: "raw48"},
	184: {Name: "End-to-End_Error", Format: "raw48"},
	187: {Name: "Reported_Uncorrect", Format: "raw48"},
	188: {Name: "Command_Timeout", Format: "raw48"},
	189: {Name: "High_Fly_Writes", Format: "raw48", HDDOnly: true},
	190: {Name: "Airflow_Temperature_Cel", Format: "tempminmax"},
	191: {Name: "G-Sense_Error_Rate", Format: "raw48", HDDOnly: true},
	192: {Name: "Power-Off_Retract_Count", Format: "raw48"},
	193: {Name: "Load_Cycle_Count", Format: "raw48", HDDOnly: true},
	194: {Name: "Temperature_Celsius", Format: "tempminmax"},
	195: {Name: "Hardware_ECC_Recovered", Format: "raw48"},
	196: {Name: "Reallocated_Event_Count", Format: "raw16"},
	197: {Name: "Current_Pending_Sector", Format: "raw48"},
	198: {Name: "Offline_Uncorrectable", Format: "raw48"},
	199: {Name: "UDMA_CRC_Error_Count", Format: "raw48"},
	200: {Name: "Multi_Zone_Error_Rate", Format: "raw48", HDDOnly: true},
	201: {Name: "Soft_Read_Error_Rate", Format: "raw48", HDDOnly: true},
	202: {Name: "Data_Address_Mark_Errs", Format: "raw48", HDDOnly: true},
	203: {Name: "Run_Out_Cancel", Format: "raw48"},
	204: {Name: "Soft_ECC_Correction", Format: "raw48"},
	205: {Name: "Thermal_Asperity_Rate", Format: "raw48"},
	206: {Name: "Flying_Height", Format: "raw48", HDDOnly: true},
	207: {Name: "Spin_High_Current", Format: "raw48", HDDOnly: true},
	208: {Name: "Spin_Buzz", Format: "raw48", HDDOnly: true},
	209: {Name: "Offline_Seek_Performnce", Format: "raw48", HDDOnly: true},
	220: {Name: "Disk_Shift", Format: "raw48", HDDOnly: true},
	221: {Name: "G-Sense_Error_Rate", Format: "raw48", HDDOnly: true},
	222: {Name: "Loaded_Hours", Format: "raw48", HDDOnly: true},
	223: {Name: "Load_Retry_Count", Format: "raw48", HDDOnly: true},
	224: {Name: "Load_Friction", Format: "raw48", HDDOnly: true},
	225: {Name: "Load_Cycle_Count", Format: "raw48", HDDOnly: true},
	226: {Name: "Load-in_Time", Format: "raw48", HDDOnly: true},
	227: {Name: "Torq-amp_Count", Format: "raw48", HDDOnly: true},
	228: {Name: "Power-off_Retract_Count", Format: "raw48"},
	230: {Name: "Head_Amplitude", Format: "raw48", HDDOnly: true},
	231: {Name: "Temperature_Celsius", Format: "raw48", HDDOnly: true},
	232: {Name: "Available_Reservd_Space", Format: "raw48"},
	233: {Name: "Media_Wearout_Indicator", Format: "raw48", SSDOnly: true},
	240: {Name: "Head_Flying_Hours", Format: "raw24", HDDOnly: true},
	241: {Name: "Total_LBAs_Written", Format: "raw48"},
	242: {Name: "Total_LBAs_Read", Format: "raw48"},
	250: {Name: "Read_Error_Retry_Rate", Format: "raw48"},
	254: {Name: "Free_Fall_Sensor", Format: "raw48", HDDOnly: true},
}

func LookupAttrDef(id uint32) (AttrDef, bool) {
	d, ok := DefaultAttrDefs[id]
	return d, ok
}

func LookupModelAttrs(model string) map[uint32]AttrDef {
	result := make(map[uint32]AttrDef)
	for _, entry := range modelDB {
		if matchModel(model, entry.ModelPattern) {
			for id, def := range entry.AttrOverrides {
				result[id] = def
			}
		}
	}
	return result
}

func matchModel(model, pattern string) bool {
	if len(pattern) == 0 {
		return false
	}
	if len(pattern) >= 2 && pattern[0] == '^' {
		pattern = pattern[1:]
	}
	mlen, plen := len(model), len(pattern)
	if plen > mlen {
		return false
	}
	for i := 0; i < plen; i++ {
		mc := model[i]
		pc := pattern[i]
		if pc == '_' && mc == ' ' {
			continue
		}
		if pc >= '0' && pc <= '9' && i+1 < plen && pattern[i+1] == ']' {
			if mc >= '0' && mc <= '9' {
				continue
			}
		}
		if pc == '[' {
			continue
		}
		if pc == ']' {
			continue
		}
		if pc == '?' && i+1 < plen {
			continue
		}
		if mc != pc {
			return false
		}
	}
	return true
}
