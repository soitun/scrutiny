package db

import (
	"github.com/analogj/scrutiny/webapp/backend/pkg/metadata"
	"github.com/analogj/scrutiny/webapp/backend/pkg/models/collector"
	"strings"
	"time"
)

type DeviceWrapper struct {
	Success bool     `json:"success"`
	Errors  []error  `json:"errors"`
	Data    []Device `json:"data"`
}

const DeviceProtocolAta = "ATA"
const DeviceProtocolScsi = "SCSI"
const DeviceProtocolNvme = "NVMe"

type Device struct {
	//GORM attributes, see: http://gorm.io/docs/conventions.html
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time

	WWN string `json:"wwn" gorm:"primary_key"`

	DeviceName     string  `json:"device_name"`
	Manufacturer   string  `json:"manufacturer"`
	ModelName      string  `json:"model_name"`
	InterfaceType  string  `json:"interface_type"`
	InterfaceSpeed string  `json:"interface_speed"`
	SerialNumber   string  `json:"serial_number"`
	Firmware       string  `json:"firmware"`
	RotationSpeed  int     `json:"rotational_speed"`
	Capacity       int64   `json:"capacity"`
	FormFactor     string  `json:"form_factor"`
	SmartSupport   bool    `json:"smart_support"`
	DeviceProtocol string  `json:"device_protocol"`
	DeviceType     string  `json:"device_type"` //device type is used for querying with -d/t flag
	SmartResults   []Smart `gorm:"foreignkey:DeviceWWN" json:"smart_results"`
}

func (dv *Device) IsAta() bool {
	return dv.DeviceProtocol == DeviceProtocolAta
}

func (dv *Device) IsScsi() bool {
	return dv.DeviceProtocol == DeviceProtocolScsi
}

func (dv *Device) IsNvme() bool {
	return dv.DeviceProtocol == DeviceProtocolNvme
}

//This method requires a device with an array of SmartResults.
//It will remove all SmartResults other than the first (the latest one)
//All removed SmartResults, will be processed, grouping SmartAtaAttribute by attribute_id
// and adding theme to an array called History.
func (dv *Device) SquashHistory() error {
	if len(dv.SmartResults) <= 1 {
		return nil //no ataHistory found. ignore
	}

	latestSmartResultSlice := dv.SmartResults[0:1]
	historicalSmartResultSlice := dv.SmartResults[1:]

	//re-assign the latest slice to the SmartResults field
	dv.SmartResults = latestSmartResultSlice

	//process the historical slice for ATA data
	if len(dv.SmartResults[0].AtaAttributes) > 0 {
		ataHistory := map[int][]SmartAtaAttribute{}
		for _, smartResult := range historicalSmartResultSlice {
			for _, smartAttribute := range smartResult.AtaAttributes {
				if _, ok := ataHistory[smartAttribute.AttributeId]; !ok {
					ataHistory[smartAttribute.AttributeId] = []SmartAtaAttribute{}
				}
				ataHistory[smartAttribute.AttributeId] = append(ataHistory[smartAttribute.AttributeId], smartAttribute)
			}
		}

		//now assign the historical slices to the AtaAttributes in the latest SmartResults
		for sandx, smartAttribute := range dv.SmartResults[0].AtaAttributes {
			if attributeHistory, ok := ataHistory[smartAttribute.AttributeId]; ok {
				dv.SmartResults[0].AtaAttributes[sandx].History = attributeHistory
			}
		}
	}

	//process the historical slice for Nvme data
	if len(dv.SmartResults[0].NvmeAttributes) > 0 {
		nvmeHistory := map[string][]SmartNvmeAttribute{}
		for _, smartResult := range historicalSmartResultSlice {
			for _, smartAttribute := range smartResult.NvmeAttributes {
				if _, ok := nvmeHistory[smartAttribute.AttributeId]; !ok {
					nvmeHistory[smartAttribute.AttributeId] = []SmartNvmeAttribute{}
				}
				nvmeHistory[smartAttribute.AttributeId] = append(nvmeHistory[smartAttribute.AttributeId], smartAttribute)
			}
		}

		//now assign the historical slices to the AtaAttributes in the latest SmartResults
		for sandx, smartAttribute := range dv.SmartResults[0].NvmeAttributes {
			if attributeHistory, ok := nvmeHistory[smartAttribute.AttributeId]; ok {
				dv.SmartResults[0].NvmeAttributes[sandx].History = attributeHistory
			}
		}
	}
	//process the historical slice for Scsi data
	if len(dv.SmartResults[0].ScsiAttributes) > 0 {
		scsiHistory := map[string][]SmartScsiAttribute{}
		for _, smartResult := range historicalSmartResultSlice {
			for _, smartAttribute := range smartResult.ScsiAttributes {
				if _, ok := scsiHistory[smartAttribute.AttributeId]; !ok {
					scsiHistory[smartAttribute.AttributeId] = []SmartScsiAttribute{}
				}
				scsiHistory[smartAttribute.AttributeId] = append(scsiHistory[smartAttribute.AttributeId], smartAttribute)
			}
		}

		//now assign the historical slices to the AtaAttributes in the latest SmartResults
		for sandx, smartAttribute := range dv.SmartResults[0].ScsiAttributes {
			if attributeHistory, ok := scsiHistory[smartAttribute.AttributeId]; ok {
				dv.SmartResults[0].ScsiAttributes[sandx].History = attributeHistory
			}
		}
	}
	return nil
}

func (dv *Device) ApplyMetadataRules() error {
	if !dv.IsAta() {
		// Scrutiny Observed thresholds not yet available for NVME or SCSI drives
		// since most SMART attributes are not present and BackBlaze data not available
		return nil
	}

	//embed metadata in the latest smart attributes object
	if len(dv.SmartResults) > 0 && len(dv.SmartResults[0].AtaAttributes) > 0 {
		for ndx, attr := range dv.SmartResults[0].AtaAttributes {
			if strings.ToUpper(attr.WhenFailed) == SmartWhenFailedFailingNow {
				//this attribute has previously failed
				dv.SmartResults[0].AtaAttributes[ndx].Status = SmartAttributeStatusFailed
				dv.SmartResults[0].AtaAttributes[ndx].StatusReason = "Attribute is failing manufacturer SMART threshold"

			} else if strings.ToUpper(attr.WhenFailed) == SmartWhenFailedInThePast {
				dv.SmartResults[0].AtaAttributes[ndx].Status = SmartAttributeStatusWarning
				dv.SmartResults[0].AtaAttributes[ndx].StatusReason = "Attribute has previously failed manufacturer SMART threshold"
			}

			if smartMetadata, ok := metadata.AtaMetadata[attr.AttributeId]; ok {
				dv.SmartResults[0].AtaAttributes[ndx].MetadataObservedThresholdStatus(smartMetadata)
			}

			//check if status is blank, set to "passed"
			if len(dv.SmartResults[0].AtaAttributes[ndx].Status) == 0 {
				dv.SmartResults[0].AtaAttributes[ndx].Status = SmartAttributeStatusPassed
			}
		}
	}
	return nil
}

func (dv *Device) UpdateFromCollectorSmartInfo(info collector.SmartInfo) error {
	dv.InterfaceSpeed = info.InterfaceSpeed.Current.String
	dv.Firmware = info.FirmwareVersion
	dv.RotationSpeed = info.RotationRate
	dv.Capacity = info.UserCapacity.Bytes
	dv.FormFactor = info.FormFactor.Name
	dv.DeviceProtocol = info.Device.Protocol
	dv.DeviceType = info.Device.Type
	if len(info.Vendor) > 0 {
		dv.Manufacturer = info.Vendor
	}

	return nil
}
