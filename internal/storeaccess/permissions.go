// Package storeaccess decides what a signed-in user may do in a store: owners
// can do everything, staff get the permissions stored on their membership.
package storeaccess

import (
	"fmt"
	"slices"
)

type Permission string

const (
	StoreView            Permission = "store.view"
	StoreStatus          Permission = "store.status"
	StoreManage          Permission = "store.manage"
	CatalogView          Permission = "catalog.view"
	CatalogManage        Permission = "catalog.manage"
	InventoryView        Permission = "inventory.view"
	InventoryUpdate      Permission = "inventory.update"
	OrdersView           Permission = "orders.view"
	OrdersAccept         Permission = "orders.accept"
	OrdersFulfil         Permission = "orders.fulfil"
	OrdersPayment        Permission = "orders.payment"
	OrdersRefund         Permission = "orders.refund"
	CustomersCommunicate Permission = "customers.communicate"
	CustomersSupport     Permission = "customers.support"
	OffersManage         Permission = "offers.manage"
	AnalyticsView        Permission = "analytics.view"
	TeamManage           Permission = "team.manage"
)

type Role string

const (
	RoleOwner      Role = "OWNER"
	RoleStoreAdmin Role = "STORE_ADMIN"
	RoleStoreStaff Role = "STORE_STAFF"
)

type PermissionInfo struct {
	Key         Permission   `json:"key"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Requires    []Permission `json:"requires,omitempty"`
}

type PermissionGroup struct {
	ID          string           `json:"id"`
	Label       string           `json:"label"`
	Permissions []PermissionInfo `json:"permissions"`
}

var groups = []PermissionGroup{
	{ID: "store", Label: "Store", Permissions: []PermissionInfo{
		{Key: StoreView, Label: "View store", Description: "See store details, hours and service area."},
		{Key: StoreStatus, Label: "Open or close store", Description: "Update the store's open/closed status for the day.",
			Requires: []Permission{StoreView}},
		{Key: StoreManage, Label: "Manage store profile",
			Description: "Edit profile, location, opening hours, service area, photos and vacation mode.",
			Requires:    []Permission{StoreView, StoreStatus}},
	}},
	{ID: "catalog", Label: "Catalogue & pricing", Permissions: []PermissionInfo{
		{Key: CatalogView, Label: "View catalogue", Description: "Browse the store's products, variants and prices."},
		{Key: CatalogManage, Label: "Manage products & pricing",
			Description: "Add, edit or remove products, categories, brands, variants, images and this store's prices.",
			Requires:    []Permission{CatalogView}},
	}},
	{ID: "inventory", Label: "Inventory", Permissions: []PermissionInfo{
		{Key: InventoryView, Label: "View inventory", Description: "See stock levels and reserved quantities.",
			Requires: []Permission{CatalogView}},
		{Key: InventoryUpdate, Label: "Update stock", Description: "Change quantities, availability and low-stock alerts.",
			Requires: []Permission{InventoryView}},
	}},
	{ID: "orders", Label: "Orders", Permissions: []PermissionInfo{
		{Key: OrdersView, Label: "View orders", Description: "See incoming orders and their details."},
		{Key: OrdersAccept, Label: "Accept or reject orders", Description: "Decide whether the store takes an order.",
			Requires: []Permission{OrdersView}},
		{Key: OrdersFulfil, Label: "Fulfil orders",
			Description: "Validate stock, pick, report unavailable items, suggest substitutes, pack, mark ready and hand over.",
			Requires:    []Permission{OrdersView}},
		{Key: OrdersPayment, Label: "Payments & invoices", Description: "Confirm payment at pickup and generate invoices.",
			Requires: []Permission{OrdersView}},
		{Key: OrdersRefund, Label: "Process refunds", Description: "Issue refunds and handle disputes.",
			Requires: []Permission{OrdersView}},
	}},
	{ID: "customers", Label: "Customers", Permissions: []PermissionInfo{
		{Key: CustomersCommunicate, Label: "Message customers", Description: "Chat with customers about their orders.",
			Requires: []Permission{OrdersView}},
		{Key: CustomersSupport, Label: "Reviews & issues", Description: "Respond to reviews and resolve customer issues.",
			Requires: []Permission{OrdersView}},
	}},
	{ID: "growth", Label: "Offers & insights", Permissions: []PermissionInfo{
		{Key: OffersManage, Label: "Manage offers", Description: "Create offers, promotions and loyalty rewards.",
			Requires: []Permission{CatalogView}},
		{Key: AnalyticsView, Label: "View analytics", Description: "See sales, inventory, customer and offer analytics."},
	}},
	{ID: "team", Label: "Team", Permissions: []PermissionInfo{
		{Key: TeamManage, Label: "Manage staff", Description: "Add, edit, pause or remove store staff in this store.",
			Requires: []Permission{StoreView}},
	}},
}

// ownerOnly lists capabilities that are never delegated to staff.
var ownerOnly = []string{
	"Retailer profile, KYC/KYB and verification",
	"Create, enable or disable stores",
	"Assign store admins",
	"Bank details, wallet, ledger, settlements and statements",
	"B2B purchasing, wholesale quotes and restocking orders",
	"Referrals",
}

var presets = map[Role][]Permission{
	RoleStoreAdmin: allPermissions(),
	RoleStoreStaff: {
		StoreView, StoreStatus,
		CatalogView,
		InventoryView, InventoryUpdate,
		OrdersView, OrdersAccept, OrdersFulfil, OrdersPayment,
		CustomersCommunicate,
	},
}

type Catalogue struct {
	Groups    []PermissionGroup     `json:"groups"`
	Presets   map[Role][]Permission `json:"presets"`
	OwnerOnly []string              `json:"owner_only"`
}

func GetCatalogue() Catalogue {
	return Catalogue{Groups: groups, Presets: presets, OwnerOnly: ownerOnly}
}

func allPermissions() []Permission {
	var out []Permission
	for _, group := range groups {
		for _, item := range group.Permissions {
			out = append(out, item.Key)
		}
	}
	return out
}

func lookup(key Permission) (PermissionInfo, bool) {
	for _, group := range groups {
		for _, item := range group.Permissions {
			if item.Key == key {
				return item, true
			}
		}
	}
	return PermissionInfo{}, false
}

// IsStaffRole reports whether role can be assigned to a staff membership.
func IsStaffRole(role Role) bool {
	return role == RoleStoreAdmin || role == RoleStoreStaff
}

// Preset returns a copy of the default permissions for a staff role.
func Preset(role Role) []Permission {
	return slices.Clone(presets[role])
}

// Normalize validates permission keys, adds the permissions each one depends
// on, drops duplicates and returns them in catalogue order.
func Normalize(keys []string) ([]Permission, error) {
	want := make(map[Permission]bool, len(keys))
	var add func(Permission) error
	add = func(key Permission) error {
		if want[key] {
			return nil
		}
		info, ok := lookup(key)
		if !ok {
			return fmt.Errorf("unknown permission %q", key)
		}
		want[key] = true
		for _, dep := range info.Requires {
			if err := add(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, key := range keys {
		if err := add(Permission(key)); err != nil {
			return nil, err
		}
	}
	out := make([]Permission, 0, len(want))
	for _, key := range allPermissions() {
		if want[key] {
			out = append(out, key)
		}
	}
	return out, nil
}
