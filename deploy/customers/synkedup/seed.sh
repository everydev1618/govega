#!/usr/bin/env bash
# seed.sh — Populate Summit Landscaping domain tables with starter data.
# Run after a full reset: bash deploy/customers/synkedup/seed.sh
#
# Uses Walt (estimating lead) for items/estimates/production rates,
# Mara (sales lead) for customers/properties/leads, June (scheduler) for
# calendar/crews, Devin (invoicing) for invoices, Hank (ops) for vendors,
# and Grace (finance) for budgets/cost codes.
#
# Each function sends one chat message with batch instructions.
# The agent creates all records via tool calls in a single turn.

set -euo pipefail

BASE="${VEGA_BASE_URL:-https://summit.synkedup.ai}"

send() {
  local agent="$1"
  local label="$2"
  local msg="$3"
  echo "  [$agent] $label..."
  resp=$(curl -sf --max-time 180 -X POST "$BASE/api/agents/$agent/chat" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg m "$msg" '{message: $m}')" 2>&1) || true
  result=$(echo "$resp" | jq -r '.response // "ERROR"' 2>/dev/null | head -c 200) || result="ERROR: $resp"
  echo "    → $result"
  sleep 2
}

# ─────────────────────────────────────────────────────────────
echo "=== Seeding Cost Codes & Divisions ==="
# ─────────────────────────────────────────────────────────────

send grace "cost codes" 'Create the following cost codes using create_cost_code. Do NOT skip any:

1. code: 01-100, name: Field Labor - Hardscape, division: Labor, division_group: Direct Costs
2. code: 01-200, name: Field Labor - Planting, division: Labor, division_group: Direct Costs
3. code: 01-300, name: Field Labor - Maintenance, division: Labor, division_group: Direct Costs
4. code: 01-400, name: Field Labor - Irrigation, division: Labor, division_group: Direct Costs
5. code: 01-500, name: Field Labor - Lighting, division: Labor, division_group: Direct Costs
6. code: 01-600, name: Field Labor - Drainage, division: Labor, division_group: Direct Costs
7. code: 02-100, name: Pavers & Hardscape Material, division: Materials, division_group: Direct Costs
8. code: 02-200, name: Plants & Trees, division: Materials, division_group: Direct Costs
9. code: 02-300, name: Mulch & Soil, division: Materials, division_group: Direct Costs
10. code: 02-400, name: Irrigation Supplies, division: Materials, division_group: Direct Costs
11. code: 02-500, name: Lighting Supplies, division: Materials, division_group: Direct Costs
12. code: 02-600, name: Drainage Supplies, division: Materials, division_group: Direct Costs
13. code: 02-700, name: Aggregates & Base, division: Materials, division_group: Direct Costs
14. code: 03-100, name: Equipment Rental, division: Equipment, division_group: Direct Costs
15. code: 03-200, name: Fuel & Maintenance, division: Equipment, division_group: Direct Costs
16. code: 04-100, name: Subcontractor - Tree Work, division: Subcontractor, division_group: Direct Costs
17. code: 04-200, name: Subcontractor - Concrete, division: Subcontractor, division_group: Direct Costs
18. code: 05-100, name: Truck Payments, division: Overhead, division_group: Indirect Costs
19. code: 05-200, name: Insurance, division: Overhead, division_group: Indirect Costs
20. code: 05-300, name: Office & Admin, division: Overhead, division_group: Indirect Costs
21. code: 05-400, name: Owner Salary, division: Overhead, division_group: Indirect Costs
22. code: 05-500, name: Marketing, division: Overhead, division_group: Indirect Costs
23. code: 06-100, name: Disposal & Dumpsters, division: Disposal, division_group: Direct Costs'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Budget ==="
# ─────────────────────────────────────────────────────────────

send grace "2026 budget" 'Create a 2026 budget using create_budget with these values:
year: 2026, name: 2026 Operating Budget, revenue_target: 850000, total_overhead: 285000, billable_hours: 4200, hourly_rate: 67.86, owner_salary: 100000, target_margin_pct: 20, notes: First full year with AI back office. Conservative target.

Then create these budget lines using create_budget_line (use the budget_id from the budget you just created):

1. description: Owner Salary, category: overhead, annual_amount: 100000, monthly_amount: 8333, cost_code: 05-400
2. description: Truck Payments (3 trucks), category: overhead, annual_amount: 36000, monthly_amount: 3000, cost_code: 05-100
3. description: General Liability + WC Insurance, category: overhead, annual_amount: 42000, monthly_amount: 3500, cost_code: 05-200
4. description: Office Rent & Utilities, category: overhead, annual_amount: 24000, monthly_amount: 2000, cost_code: 05-300
5. description: Office Manager Salary, category: overhead, annual_amount: 45000, monthly_amount: 3750, cost_code: 05-300
6. description: Software & CRM, category: overhead, annual_amount: 6000, monthly_amount: 500, cost_code: 05-300
7. description: Marketing & Advertising, category: overhead, annual_amount: 18000, monthly_amount: 1500, cost_code: 05-500
8. description: Equipment Maintenance & Repair, category: overhead, annual_amount: 14000, monthly_amount: 1167, cost_code: 03-200'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Items Catalog ==="
# ─────────────────────────────────────────────────────────────

send walt "hardscape materials" 'Create all items using create_item. Do NOT skip any:

1. name: Techo-Bloc Blu 60 Paver, category: material, unit: sq_ft, cost: 4.25, price: 8.50, supplier: Techo-Bloc, taxable: true
2. name: Techo-Bloc Blu 80 Slab, category: material, unit: sq_ft, cost: 6.50, price: 13.00, supplier: Techo-Bloc, taxable: true
3. name: Belgard Mega Arbel Paver, category: material, unit: sq_ft, cost: 3.75, price: 7.50, supplier: Belgard, taxable: true
4. name: Belgard Dublin Cobble, category: material, unit: sq_ft, cost: 4.00, price: 8.00, supplier: Belgard, taxable: true
5. name: EP Henry Coventry Stone, category: material, unit: sq_ft, cost: 5.00, price: 10.00, supplier: EP Henry, taxable: true
6. name: Natural Flagstone — irregular, category: material, unit: sq_ft, cost: 6.00, price: 12.00, supplier: Local Quarry, taxable: true
7. name: Versa-Lok Retaining Wall Block, category: material, unit: sq_ft, cost: 8.50, price: 17.00, supplier: Versa-Lok, taxable: true
8. name: Belgard Celtik Wall Block, category: material, unit: sq_ft, cost: 7.00, price: 14.00, supplier: Belgard, taxable: true
9. name: Allan Block AB Classic, category: material, unit: sq_ft, cost: 7.50, price: 15.00, supplier: Allan Block, taxable: true
10. name: Compacted Aggregate Base (2A Modified), category: material, unit: cu_yd, cost: 28.00, price: 45.00, supplier: Local Quarry, taxable: true'

send walt "base and drainage materials" 'Create all items using create_item. Do NOT skip any:

1. name: Concrete Sand — bedding, category: material, unit: cu_yd, cost: 32.00, price: 50.00, supplier: Local Quarry, taxable: true
2. name: Polymeric Sand — Techniseal HP, category: material, unit: bag, cost: 22.00, price: 35.00, supplier: Techo-Bloc, taxable: true
3. name: Geotextile Fabric — non-woven, category: material, unit: sq_ft, cost: 0.15, price: 0.35, supplier: US Fabrics, taxable: true
4. name: Geogrid — Tensar BX1200, category: material, unit: sq_ft, cost: 0.85, price: 1.75, supplier: Tensar, taxable: true
5. name: Drainage Pipe — 4in corrugated, category: material, unit: lin_ft, cost: 1.25, price: 2.50, supplier: ADS, taxable: true
6. name: Drainage Pipe — 4in solid PVC, category: material, unit: lin_ft, cost: 2.00, price: 4.00, supplier: ADS, taxable: true
7. name: Drain Basin — NDS 12in catch basin, category: material, unit: each, cost: 45.00, price: 90.00, supplier: NDS, taxable: true
8. name: French Drain Stone — #57 clean, category: material, unit: cu_yd, cost: 35.00, price: 55.00, supplier: Local Quarry, taxable: true
9. name: Rebar — #4 (1/2in), category: material, unit: lin_ft, cost: 0.75, price: 1.50, supplier: Local Supply, taxable: true
10. name: Concrete — ready mix 4000 PSI, category: material, unit: cu_yd, cost: 145.00, price: 225.00, supplier: Ready Mix Co, taxable: true'

send walt "landscape materials" 'Create all items using create_item. Do NOT skip any:

1. name: Hardwood Mulch — double-ground, category: material, unit: cu_yd, cost: 28.00, price: 55.00, supplier: Local Mulch, taxable: true
2. name: Black Dyed Mulch, category: material, unit: cu_yd, cost: 32.00, price: 65.00, supplier: Local Mulch, taxable: true
3. name: Playground Mulch — certified, category: material, unit: cu_yd, cost: 38.00, price: 75.00, supplier: Local Mulch, taxable: true
4. name: River Rock — 1-3in, category: material, unit: cu_yd, cost: 55.00, price: 95.00, supplier: Local Quarry, taxable: true
5. name: Topsoil — screened, category: material, unit: cu_yd, cost: 22.00, price: 45.00, supplier: Local Supply, taxable: true
6. name: Compost — organic, category: material, unit: cu_yd, cost: 30.00, price: 55.00, supplier: Local Supply, taxable: true
7. name: Sod — tall fescue, category: material, unit: sq_ft, cost: 0.35, price: 0.85, supplier: Sod Farm, taxable: true
8. name: Seed Mix — sun/shade blend, category: material, unit: bag, cost: 65.00, price: 120.00, supplier: Pennington, taxable: true
9. name: Straw Blanket — erosion control, category: material, unit: each, cost: 45.00, price: 85.00, supplier: US Fabrics, taxable: true
10. name: Landscape Edging — aluminum, category: material, unit: lin_ft, cost: 2.50, price: 5.00, supplier: Local Supply, taxable: true'

send walt "plants and lighting" 'Create all items using create_item. Do NOT skip any:

1. name: Shrub — 3 gallon (generic), category: material, unit: each, cost: 18.00, price: 45.00, supplier: Nursery, taxable: true
2. name: Shrub — 5 gallon (generic), category: material, unit: each, cost: 30.00, price: 65.00, supplier: Nursery, taxable: true
3. name: Ornamental Tree — 6-8ft B&B, category: material, unit: each, cost: 175.00, price: 350.00, supplier: Nursery, taxable: true
4. name: Shade Tree — 2-2.5in caliper B&B, category: material, unit: each, cost: 275.00, price: 550.00, supplier: Nursery, taxable: true
5. name: Perennial — 1 gallon, category: material, unit: each, cost: 8.00, price: 22.00, supplier: Nursery, taxable: true
6. name: Annual Flat — 48 count, category: material, unit: each, cost: 18.00, price: 40.00, supplier: Nursery, taxable: true
7. name: Ornamental Grass — 3 gallon, category: material, unit: each, cost: 15.00, price: 35.00, supplier: Nursery, taxable: true
8. name: Low-voltage LED Path Light, category: material, unit: each, cost: 45.00, price: 110.00, supplier: FX Luminaire, taxable: true
9. name: Low-voltage LED Spotlight, category: material, unit: each, cost: 65.00, price: 150.00, supplier: FX Luminaire, taxable: true
10. name: Low-voltage LED Well Light, category: material, unit: each, cost: 85.00, price: 195.00, supplier: FX Luminaire, taxable: true'

send walt "irrigation and features" 'Create all items using create_item. Do NOT skip any:

1. name: Low-voltage Transformer — 300W, category: material, unit: each, cost: 225.00, price: 450.00, supplier: FX Luminaire, taxable: true
2. name: Low-voltage Wire — 12/2, category: material, unit: lin_ft, cost: 0.45, price: 1.00, supplier: FX Luminaire, taxable: true
3. name: Irrigation Rotor — Hunter PGP, category: material, unit: each, cost: 18.00, price: 40.00, supplier: Hunter, taxable: true
4. name: Irrigation Spray Head — Hunter PS Ultra, category: material, unit: each, cost: 8.00, price: 22.00, supplier: Hunter, taxable: true
5. name: Irrigation Valve — 1in, category: material, unit: each, cost: 22.00, price: 50.00, supplier: Hunter, taxable: true
6. name: Irrigation Controller — Hunter Pro-HC, category: material, unit: each, cost: 175.00, price: 350.00, supplier: Hunter, taxable: true
7. name: Irrigation Pipe — 1in poly, category: material, unit: lin_ft, cost: 0.55, price: 1.25, supplier: Hunter, taxable: true
8. name: Drip Line — 0.5 GPH 12in spacing, category: material, unit: lin_ft, cost: 0.35, price: 0.85, supplier: Hunter, taxable: true
9. name: Fire Pit Kit — Belgard Weston, category: material, unit: each, cost: 850.00, price: 1800.00, supplier: Belgard, taxable: true
10. name: Outdoor Kitchen — modular L-shape, category: material, unit: each, cost: 4500.00, price: 9500.00, supplier: RTA Outdoor, taxable: true'

send walt "labor rates" 'Create all items using create_item. Do NOT skip any:

1. name: Field Labor — Hardscape, category: labor, unit: hour, cost: 22.00, price: 67.50, supplier: , taxable: false, notes: Burdened rate includes wages taxes benefits WC
2. name: Field Labor — Planting, category: labor, unit: hour, cost: 20.00, price: 60.00, supplier: , taxable: false, notes: Burdened rate includes wages taxes benefits WC
3. name: Field Labor — Maintenance/Mowing, category: labor, unit: hour, cost: 18.00, price: 55.00, supplier: , taxable: false, notes: Burdened rate includes wages taxes benefits WC
4. name: Field Labor — Irrigation, category: labor, unit: hour, cost: 25.00, price: 75.00, supplier: , taxable: false, notes: Specialized skill premium
5. name: Field Labor — Lighting, category: labor, unit: hour, cost: 25.00, price: 75.00, supplier: , taxable: false, notes: Specialized skill premium
6. name: Field Labor — Drainage, category: labor, unit: hour, cost: 22.00, price: 67.50, supplier: , taxable: false, notes: Burdened rate includes wages taxes benefits WC
7. name: Crew Mobilization, category: labor, unit: each, cost: 75.00, price: 150.00, supplier: , taxable: false, notes: Per job site visit — truck fuel and setup time
8. name: Site Cleanup/Debris Removal, category: labor, unit: hour, cost: 18.00, price: 55.00, supplier: , taxable: false
9. name: Design/Consultation, category: labor, unit: hour, cost: 0, price: 125.00, supplier: , taxable: false, notes: Waived if project moves forward
10. name: Project Management, category: labor, unit: hour, cost: 0, price: 95.00, supplier: , taxable: false'

send walt "equipment and disposal" 'Create all items using create_item. Do NOT skip any:

1. name: Mini Excavator Rental — CAT 303.5, category: equipment, unit: hour, cost: 85.00, price: 150.00, supplier: United Rentals, taxable: false
2. name: Skid Steer Rental — Bobcat S650, category: equipment, unit: hour, cost: 75.00, price: 135.00, supplier: United Rentals, taxable: false
3. name: Plate Compactor — rental, category: equipment, unit: hour, cost: 15.00, price: 35.00, supplier: United Rentals, taxable: false
4. name: Dump Trailer Load — delivery, category: equipment, unit: each, cost: 85.00, price: 175.00, supplier: Internal, taxable: false
5. name: Dumpster — 10yd rolloff, category: disposal, unit: each, cost: 350.00, price: 550.00, supplier: Waste Mgmt, taxable: false
6. name: Dumpster — 20yd rolloff, category: disposal, unit: each, cost: 475.00, price: 700.00, supplier: Waste Mgmt, taxable: false
7. name: Concrete Saw Rental, category: equipment, unit: hour, cost: 25.00, price: 50.00, supplier: United Rentals, taxable: false
8. name: Sod Cutter Rental, category: equipment, unit: hour, cost: 35.00, price: 65.00, supplier: United Rentals, taxable: false
9. name: Stump Grinder Rental, category: equipment, unit: hour, cost: 65.00, price: 120.00, supplier: United Rentals, taxable: false
10. name: Tree Removal — subcontractor, category: subcontractor, unit: each, cost: 0, price: 0, supplier: , taxable: false, notes: Price varies — get quote per job. Typical range $500-$3000 per tree.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Crew Members & Crews ==="
# ─────────────────────────────────────────────────────────────

send june "crew members" 'Create the following crew members using create_crew_member. Do NOT skip any:

1. name: Carlos Rivera, role: foreman, phone: 555-0201, hourly_rate: 35.00, skills: hardscape,grading,equipment, notes: 10 years experience. A-crew lead.
2. name: Jake Thompson, role: crew_lead, phone: 555-0202, hourly_rate: 28.00, skills: hardscape,planting, notes: 5 years. Reliable. B-crew lead.
3. name: Miguel Santos, role: laborer, phone: 555-0203, hourly_rate: 20.00, skills: hardscape,cleanup
4. name: Devon Williams, role: laborer, phone: 555-0204, hourly_rate: 20.00, skills: hardscape,drainage
5. name: Ryan O Brien, role: laborer, phone: 555-0205, hourly_rate: 18.00, skills: planting,mulch,mowing
6. name: Marcus Chen, role: laborer, phone: 555-0206, hourly_rate: 18.00, skills: mowing,planting,cleanup
7. name: Tyler Brooks, role: foreman, phone: 555-0207, hourly_rate: 32.00, skills: irrigation,lighting,drainage, notes: Irrigation specialist. C-crew lead.
8. name: Alex Petrov, role: laborer, phone: 555-0208, hourly_rate: 22.00, skills: irrigation,lighting
9. name: Sam Washington, role: scheduler, phone: 555-0209, email: sam@summitlandscaping.com, hourly_rate: 25.00, skills: scheduling,routing
10. name: Trevor Fountain, role: owner, phone: 555-0200, email: trevor@summitlandscaping.com, hourly_rate: 0, notes: Owner. Not billed as field labor.'

send june "crews" 'Create the following crews using create_crew:

1. name: A-Crew, foreman_id: 1, member_ids: [1,3,4], truck: F-350 #1 + 16ft trailer, specialties: hardscape,grading,retaining_walls
2. name: B-Crew, foreman_id: 2, member_ids: [2,5,6], truck: F-250 #2 + open trailer, specialties: planting,mulch,maintenance,cleanup
3. name: C-Crew, foreman_id: 7, member_ids: [7,8], truck: Sprinter Van #3, specialties: irrigation,lighting,drainage'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Vendors ==="
# ─────────────────────────────────────────────────────────────

send hank "vendors" 'Create the following vendors using create_vendor. Do NOT skip any:

1. name: Belgard Supply, contact_name: Tom Peters, phone: 555-0301, email: tom@belgard.com, address: 500 Industrial Blvd, specialty: pavers,retaining walls, payment_terms: Net 30, account_number: BEL-4521
2. name: Techo-Bloc Dealer — Central PA, contact_name: Lisa Braun, phone: 555-0302, email: lisa@techobloc.com, specialty: pavers,slabs,polymeric sand, payment_terms: Net 30, account_number: TB-1187
3. name: Mountain View Quarry, contact_name: Bill Harper, phone: 555-0303, specialty: aggregate,base material,flagstone,drain stone, payment_terms: COD, notes: Delivers next day. Min 5 cu yd per load.
4. name: Green Valley Nursery, contact_name: Maria Gonzalez, phone: 555-0304, email: maria@greenvalley.com, specialty: shrubs,trees,perennials,annuals, payment_terms: Net 15, account_number: GVN-220
5. name: Local Mulch & Soil, contact_name: Steve Kurtz, phone: 555-0305, specialty: mulch,topsoil,compost, payment_terms: COD, notes: Bulk pricing at 20+ yards. Free delivery over 10 yards.
6. name: Hunter Industries Distributor, contact_name: Dave Palmer, phone: 555-0306, email: dave@hunterdist.com, specialty: irrigation,controllers,valves, payment_terms: Net 30, account_number: HID-890
7. name: FX Luminaire Dealer, contact_name: Karen White, phone: 555-0307, email: karen@fxlum.com, specialty: landscape lighting,LED,transformers, payment_terms: Net 30, account_number: FXL-442
8. name: United Rentals — Altoona, contact_name: Front Desk, phone: 555-0308, specialty: excavator,skid steer,compactor,saw rental, payment_terms: Credit Account, account_number: UR-7761
9. name: Waste Management — Blair County, contact_name: Dispatch, phone: 555-0309, specialty: dumpster,rolloff,debris disposal, payment_terms: Net 30, account_number: WM-3344
10. name: Treetop Arborists, contact_name: Dan McKay, phone: 555-0310, email: dan@treetop.com, specialty: tree removal,pruning,stump grinding, payment_terms: Per Job, notes: Our go-to tree sub. Insured and licensed.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Customers & Properties ==="
# ─────────────────────────────────────────────────────────────

send mara "customers batch 1" 'Create the following customers using create_customer. Do NOT skip any:

1. name: Anderson Family, contact_name: Jim Anderson, email: jim.anderson@gmail.com, phone: 555-1001, address: 142 Oak Lane, city: Altoona, state: PA, zip: 16601, source: referral, status: active, tags: residential,repeat, payment_method: card_on_file, notes: Great customer. Referred the Hendersons.
2. name: Henderson Residence, contact_name: Sarah Henderson, email: sarah.h@outlook.com, phone: 555-1002, address: 88 Maple Court, city: Altoona, state: PA, zip: 16601, source: referral, status: active, tags: residential, payment_method: card_on_file, notes: Referred by the Andersons.
3. name: Richardson Estate, contact_name: Mark Richardson, email: mark@richardsonlaw.com, phone: 555-1003, address: 2200 Summit Drive, city: Hollidaysburg, state: PA, zip: 16648, source: website, status: active, tags: residential,high-value, payment_method: check, notes: Attorney. Large property. Budget-conscious but pays on time.
4. name: Clearview HOA, contact_name: Board President — Diane Keller, email: board@clearviewhoa.org, phone: 555-1004, address: 100 Clearview Blvd, city: Altoona, state: PA, zip: 16602, source: door_hanger, status: active, tags: commercial,HOA,maintenance, payment_method: ach, notes: 45-unit townhome community. Seasonal mowing + spring/fall cleanup.
5. name: Mountain Valley Church, contact_name: Pastor Dave, email: office@mvchurch.org, phone: 555-1005, address: 340 Church Road, city: Duncansville, state: PA, zip: 16635, source: google, status: active, tags: commercial,non-profit, payment_method: check, notes: Large campus. Annual mulch + seasonal color.'

send mara "customers batch 2" 'Create the following customers using create_customer. Do NOT skip any:

1. name: Blair County Medical Center, contact_name: Facilities — Rob Torres, email: rtorres@bcmc.org, phone: 555-1006, address: 1200 Hospital Drive, city: Altoona, state: PA, zip: 16601, source: repeat, status: active, tags: commercial,medical,maintenance, payment_method: ach, notes: Year-round maintenance contract. Snow removal in winter.
2. name: Tuscany Bistro, contact_name: Marco Vitale, email: marco@tuscanybistro.com, phone: 555-1007, address: 55 Main Street, city: Altoona, state: PA, zip: 16601, source: google, status: active, tags: commercial,restaurant, payment_method: card_on_file, notes: Outdoor patio area. Wants seasonal plantings.
3. name: Williams Residence, contact_name: Dave Williams, phone: 555-1008, address: 720 Birch Street, city: Altoona, state: PA, zip: 16601, source: yard_sign, status: prospect, tags: residential, notes: Saw our sign at the Anderson job. Wants a patio quote.
4. name: Kowalski Property, contact_name: Ed Kowalski, phone: 555-1009, address: 445 Pine Ridge Road, city: Hollidaysburg, state: PA, zip: 16648, source: social, status: prospect, tags: residential, notes: Facebook inquiry. Retaining wall and drainage work.
5. name: Sunrise Senior Living, contact_name: Facilities — Janet Marsh, email: jmarsh@sunrisesenior.com, phone: 555-1010, address: 800 Sunrise Boulevard, city: Altoona, state: PA, zip: 16602, source: referral, status: active, tags: commercial,senior-living,maintenance, payment_method: ach, notes: Referred by Blair County Medical. Seasonal maintenance contract.'

send mara "properties" 'Create properties for our customers using create_property:

1. customer_id: 1, address: 142 Oak Lane, city: Altoona, state: PA, zip: 16601, lot_size_sqft: 12000, lawn_sqft: 6000, bed_sqft: 1800, hardscape_sqft: 1200, tags: backyard_patio,fire_pit, notes: Existing patio from 2024. Fire pit added 2025.
2. customer_id: 2, address: 88 Maple Court, city: Altoona, state: PA, zip: 16601, lot_size_sqft: 9500, lawn_sqft: 5500, bed_sqft: 1200, tags: corner_lot, notes: Corner lot. Good visibility for yard sign.
3. customer_id: 3, address: 2200 Summit Drive, city: Hollidaysburg, state: PA, zip: 16648, lot_size_sqft: 45000, lawn_sqft: 25000, bed_sqft: 5000, hardscape_sqft: 3000, tags: large_property,slope,retaining_wall, notes: 1+ acre. Steep grade on south side. Existing retaining wall needs replacement.
4. customer_id: 4, address: 100 Clearview Blvd, city: Altoona, state: PA, zip: 16602, lot_size_sqft: 200000, lawn_sqft: 120000, bed_sqft: 15000, tags: HOA,common_areas,45_units, notes: Common areas only. Individual units maintain their own front yards.
5. customer_id: 5, address: 340 Church Road, city: Duncansville, state: PA, zip: 16635, lot_size_sqft: 80000, lawn_sqft: 50000, bed_sqft: 8000, tags: parking_islands,entrance, notes: Main entrance beds + 12 parking lot islands.
6. customer_id: 6, address: 1200 Hospital Drive, city: Altoona, state: PA, zip: 16601, lot_size_sqft: 150000, lawn_sqft: 60000, bed_sqft: 12000, tags: medical,year_round, notes: High visibility. Must always look immaculate.
7. customer_id: 7, address: 55 Main Street, city: Altoona, state: PA, zip: 16601, lot_size_sqft: 3000, bed_sqft: 400, hardscape_sqft: 600, tags: patio,commercial,downtown, notes: Small outdoor dining area. Seasonal planters.
8. customer_id: 8, address: 720 Birch Street, city: Altoona, state: PA, zip: 16601, lot_size_sqft: 10000, lawn_sqft: 6000, tags: prospect, notes: Not surveyed yet.
9. customer_id: 9, address: 445 Pine Ridge Road, city: Hollidaysburg, state: PA, zip: 16648, lot_size_sqft: 18000, lawn_sqft: 8000, tags: prospect,slope, notes: Steep side yard. Drainage issues reported.
10. customer_id: 10, address: 800 Sunrise Boulevard, city: Altoona, state: PA, zip: 16602, lot_size_sqft: 100000, lawn_sqft: 40000, bed_sqft: 10000, tags: senior_living,accessible, notes: ADA-accessible paths. Raised planters.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Sales Leads ==="
# ─────────────────────────────────────────────────────────────

send mara "leads" 'Create the following sales leads using create_sales_lead:

1. name: Dave Williams, customer_id: 8, phone: 555-1008, source: yard_sign, status: qualified, estimated_value: 8500, job_type: patio, property_address: 720 Birch Street, assigned_to: mara, notes: Wants 16x20 paver patio. Saw sign at Anderson job. Site visit scheduled.
2. name: Ed Kowalski, customer_id: 9, phone: 555-1009, source: social, status: contacted, estimated_value: 12000, job_type: retaining_wall, property_address: 445 Pine Ridge Road, assigned_to: mara, notes: Facebook inquiry. Retaining wall 60ft + French drain. Sent initial info.
3. name: Diane Keller — Phase 2, customer_id: 4, source: repeat, status: estimate_scheduled, estimated_value: 35000, job_type: planting, property_address: 100 Clearview Blvd, assigned_to: walt, notes: HOA board approved budget for entrance bed renovation. Walt doing walkthrough Thursday.
4. name: Unknown — Website Lead, source: website, status: new, estimated_value: 0, job_type: , property_address: , assigned_to: , notes: Form submission. No details yet. Need to call back ASAP.
5. name: Marco Vitale — Patio Expansion, customer_id: 7, source: repeat, status: proposal_sent, estimated_value: 6200, job_type: patio, property_address: 55 Main Street, assigned_to: walt, notes: Wants to extend outdoor dining. Proposal sent 3/28, follow up this week.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Jobs ==="
# ─────────────────────────────────────────────────────────────

send walt "active jobs" 'Create the following jobs using track_job:

1. customer_name: Anderson Family, property_address: 142 Oak Lane, job_type: lighting, stage: job_in_progress, owner_agent: hank, notes: 8-fixture LED path and accent lighting. Transformer installed. Wiring 60% complete., estimate_total: 4200
2. customer_name: Clearview HOA, property_address: 100 Clearview Blvd, job_type: maintenance, stage: job_scheduled, owner_agent: june, notes: Spring cleanup — 45 units common areas. Mulch + bed edging + mowing start., estimate_total: 8500
3. customer_name: Blair County Medical Center, property_address: 1200 Hospital Drive, job_type: mulch, stage: materials_ordered, owner_agent: hank, notes: Annual spring mulch. 85 cu yd black dyed. Delivery scheduled 4/7., estimate_total: 6800
4. customer_name: Henderson Residence, property_address: 88 Maple Court, job_type: planting, stage: job_completed, owner_agent: june, notes: Front bed renovation. 12 shrubs + 24 perennials + mulch. Completed 3/25., estimate_total: 3200, actual_total: 2950
5. customer_name: Mountain Valley Church, property_address: 340 Church Road, job_type: mulch, stage: invoice_sent, owner_agent: devin, notes: Spring mulch + seasonal color. 40 cu yd hardwood + 8 flats annuals. Invoiced 3/28., estimate_total: 4100, actual_total: 3800
6. customer_name: Tuscany Bistro, property_address: 55 Main Street, job_type: planting, stage: job_sold, owner_agent: walt, notes: Seasonal planters for outdoor dining. 6 large planters + soil + plants., estimate_total: 1800
7. customer_name: Richardson Estate, property_address: 2200 Summit Drive, job_type: retaining_wall, stage: estimate_created, owner_agent: walt, notes: 80ft retaining wall replacement. 4ft height. Geogrid required. Need to send proposal., estimate_total: 28500
8. customer_name: Sunrise Senior Living, property_address: 800 Sunrise Boulevard, job_type: maintenance, stage: job_scheduled, owner_agent: june, notes: Weekly mowing + bi-weekly bed maintenance. Season start 4/7., estimate_total: 18000'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Follow-ups ==="
# ─────────────────────────────────────────────────────────────

send mara "follow-ups" 'Create the following follow-ups using add_follow_up:

1. agent: mara, target_type: lead, target_name: Dave Williams, action: call, due_date: 2026-04-03, notes: Confirm site visit time for patio quote
2. agent: mara, target_type: lead, target_name: Ed Kowalski, action: call, due_date: 2026-04-03, notes: Second touch — schedule site visit for retaining wall
3. agent: mara, target_type: lead, target_name: Unknown — Website Lead, action: call, due_date: 2026-04-02, notes: URGENT — call back within 5 minutes of seeing this
4. agent: walt, target_type: lead, target_name: Marco Vitale — Patio Expansion, action: call, due_date: 2026-04-04, notes: Follow up on proposal sent 3/28. Check if he has questions.
5. agent: devin, target_type: invoice, target_name: Mountain Valley Church, action: reminder, due_date: 2026-04-07, notes: Payment reminder — invoice sent 3/28, due Net 15
6. agent: hank, target_type: job, target_name: Blair County Medical Center, action: reminder, due_date: 2026-04-06, notes: Confirm mulch delivery for 4/7. Call Local Mulch.
7. agent: june, target_type: job, target_name: Clearview HOA, action: reminder, due_date: 2026-04-04, notes: Finalize crew assignments for spring cleanup start'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Calendar Events ==="
# ─────────────────────────────────────────────────────────────

send june "calendar" 'Create the following calendar events using create_calendar_event:

1. title: Anderson Lighting — finish wiring, event_type: job, date: 2026-04-03, start_time: 08:00, end_time: 14:00, crew_id: 3, job_id: 1, customer_id: 1, status: scheduled, notes: C-crew. Finish wiring + test all fixtures.
2. title: Clearview HOA — Spring Cleanup Day 1, event_type: job, date: 2026-04-07, start_time: 07:00, end_time: 16:00, crew_id: 2, job_id: 2, customer_id: 4, status: scheduled, notes: B-crew. Start with common area beds. Edging + cleanup.
3. title: Clearview HOA — Spring Cleanup Day 2, event_type: job, date: 2026-04-08, start_time: 07:00, end_time: 16:00, crew_id: 2, job_id: 2, customer_id: 4, status: scheduled, notes: B-crew. Mulch spread + mowing pass.
4. title: Medical Center Mulch Delivery, event_type: delivery, date: 2026-04-07, start_time: 06:30, end_time: 07:30, job_id: 3, customer_id: 6, status: scheduled, notes: 85 cu yd black dyed mulch. Local Mulch delivering.
5. title: Medical Center Mulch Install — Day 1, event_type: job, date: 2026-04-07, start_time: 08:00, end_time: 16:00, crew_id: 1, job_id: 3, customer_id: 6, status: scheduled, notes: A-crew. Front entrance + parking islands.
6. title: Medical Center Mulch Install — Day 2, event_type: job, date: 2026-04-08, start_time: 08:00, end_time: 16:00, crew_id: 1, job_id: 3, customer_id: 6, status: scheduled, notes: A-crew. Side beds + rear areas.
7. title: Tuscany Bistro Planter Install, event_type: job, date: 2026-04-09, start_time: 09:00, end_time: 13:00, crew_id: 2, job_id: 6, customer_id: 7, status: scheduled, notes: B-crew. Install 6 planters before lunch rush.
8. title: Williams Patio — Site Visit, event_type: consultation, date: 2026-04-04, start_time: 10:00, end_time: 11:00, customer_id: 8, status: scheduled, notes: Walt + Mara. Measure and discuss patio options.
9. title: Sunrise Senior Living — Season Start, event_type: job, date: 2026-04-07, start_time: 07:00, end_time: 15:00, crew_id: 2, job_id: 8, customer_id: 10, status: scheduled, notes: First mow of season + bed cleanup.
10. title: Richardson Wall — Site Visit, event_type: consultation, date: 2026-04-05, start_time: 14:00, end_time: 15:30, customer_id: 3, status: scheduled, notes: Walt measuring for retaining wall proposal. Bring laser level.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Invoices ==="
# ─────────────────────────────────────────────────────────────

send devin "invoices" 'Create the following invoices using create_invoice:

1. invoice_number: INV-2026-001, customer_id: 2, job_id: 4, status: paid, subtotal: 2950.00, tax: 177.00, total: 3127.00, deposit_applied: 1600.00, amount_due: 0, issued_date: 2026-03-26, due_date: 2026-04-09, paid_date: 2026-03-28, payment_method: card, notes: Henderson front bed renovation. Paid in full — card on file.
2. invoice_number: INV-2026-002, customer_id: 5, job_id: 5, status: sent, subtotal: 3800.00, tax: 228.00, total: 4028.00, deposit_applied: 0, amount_due: 4028.00, issued_date: 2026-03-28, due_date: 2026-04-11, notes: Mountain Valley Church spring mulch + color. Net 15.
3. invoice_number: INV-2026-003, customer_id: 6, status: draft, subtotal: 2200.00, tax: 0, total: 2200.00, deposit_applied: 0, amount_due: 2200.00, issued_date: , due_date: , notes: Blair County Medical — March maintenance. Need to finalize and send.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Production Rates ==="
# ─────────────────────────────────────────────────────────────

send walt "production rates" 'Record the following baseline production rates using record_production_rate:

1. job_type: patio, unit: sq_ft, estimated_hours_per_unit: 0.08, actual_hours_per_unit: 0.09, job_name: Industry baseline, notes: Includes base prep and paver install. Standard residential.
2. job_type: retaining_wall, unit: sq_ft, estimated_hours_per_unit: 0.12, actual_hours_per_unit: 0.14, job_name: Industry baseline, notes: Block wall with geogrid. Height under 4ft.
3. job_type: planting, unit: each, estimated_hours_per_unit: 0.15, actual_hours_per_unit: 0.18, job_name: Industry baseline, notes: 3-5 gallon shrubs with bed prep and mulch.
4. job_type: mulch, unit: cu_yd, estimated_hours_per_unit: 0.25, actual_hours_per_unit: 0.22, job_name: Industry baseline, notes: Spread only — beds already edged.
5. job_type: irrigation_install, unit: each, estimated_hours_per_unit: 0.20, actual_hours_per_unit: 0.25, job_name: Industry baseline, notes: New system, includes trenching and backfill.
6. job_type: drainage, unit: lin_ft, estimated_hours_per_unit: 0.10, actual_hours_per_unit: 0.12, job_name: Industry baseline, notes: French drain with pipe and stone.
7. job_type: lighting, unit: each, estimated_hours_per_unit: 0.50, actual_hours_per_unit: 0.55, job_name: Industry baseline, notes: Path/spot light with wiring. Excludes transformer.
8. job_type: sod, unit: sq_ft, estimated_hours_per_unit: 0.015, actual_hours_per_unit: 0.018, job_name: Industry baseline, notes: Includes soil prep and sod install.
9. job_type: cleanup, unit: sq_ft, estimated_hours_per_unit: 0.03, actual_hours_per_unit: 0.025, job_name: Industry baseline, notes: Leaf and debris cleanup, blow and haul.
10. job_type: tree_install, unit: each, estimated_hours_per_unit: 1.5, actual_hours_per_unit: 2.0, job_name: Industry baseline, notes: 2-3 inch caliper B&B tree with staking.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=== Seeding Estimates ==="
# ─────────────────────────────────────────────────────────────

send walt "open estimates" 'Create the following estimates using create_estimate:

1. customer_id: 3, property_id: 3, job_id: 7, title: Richardson Retaining Wall Replacement, status: draft, line_items: [{"desc":"Versa-Lok wall block","qty":1360,"unit":"sq_ft","unit_price":17.00,"total":23120},{"desc":"Geogrid","qty":640,"unit":"sq_ft","unit_price":1.75,"total":1120},{"desc":"Aggregate base","qty":24,"unit":"cu_yd","unit_price":45.00,"total":1080},{"desc":"Field Labor — Hardscape","qty":160,"unit":"hours","unit_price":67.50,"total":10800},{"desc":"Excavator rental","qty":16,"unit":"hours","unit_price":150.00,"total":2400},{"desc":"Mobilization","qty":1,"unit":"each","unit_price":150.00,"total":150}], subtotal: 38670, tax: 1513, total: 40183, margin_pct: 22, deposit_pct: 50, valid_until: 2026-04-30, notes: 80ft x 4ft wall replacement. Includes demo of existing. Geogrid every 2 courses.

2. customer_id: 7, property_id: 7, job_id: 6, title: Tuscany Bistro Seasonal Planters, status: sent, line_items: [{"desc":"Large planters + soil","qty":6,"unit":"each","unit_price":150.00,"total":900},{"desc":"Annuals","qty":4,"unit":"flats","unit_price":40.00,"total":160},{"desc":"Perennials","qty":12,"unit":"each","unit_price":22.00,"total":264},{"desc":"Field Labor — Planting","qty":6,"unit":"hours","unit_price":60.00,"total":360},{"desc":"Mobilization","qty":1,"unit":"each","unit_price":150.00,"total":150}], subtotal: 1834, tax: 79, total: 1913, margin_pct: 35, deposit_pct: 0, valid_until: 2026-04-15, notes: 6 large seasonal planters for outdoor dining area. No deposit — existing customer.'

# ─────────────────────────────────────────────────────────────
echo ""
echo "=========================================="
echo "  Seed complete!"
echo "=========================================="
echo ""
echo "Summary of seeded data:"
echo "  - 23 cost codes across 6 divisions"
echo "  - 1 annual budget with 8 line items"
echo "  - 70 catalog items (materials, labor, equipment, disposal)"
echo "  - 10 crew members + 3 crews"
echo "  - 10 vendors"
echo "  - 10 customers + 10 properties"
echo "  - 5 sales leads"
echo "  - 8 active jobs across all lifecycle stages"
echo "  - 7 follow-ups"
echo "  - 10 calendar events for the next week"
echo "  - 3 invoices (1 paid, 1 sent, 1 draft)"
echo "  - 10 baseline production rates"
echo "  - 2 estimates (1 draft, 1 sent)"
echo ""
