# Spec Delta

## Purpose

The product site presents the sdlc plugin: what it does, how to install and set it up, and what each skill and feature gives.

## ADDED Requirements

### Requirement: Setup block on the home page
The home page SHALL show a setup block between the hero and the workflow block, with 3 steps, the Setup tab image and a link to the setup part of the getting-started guide.

#### Scenario: Block order
- **WHEN** a reader opens the home page
- **THEN** the setup block shows after the hero and before the workflow block

#### Scenario: Guide link
- **WHEN** a reader selects the guide link of the setup block
- **THEN** the browser opens `/sdlc-plugin/docs/getting-started/#2-set-up-a-project`

### Requirement: Dashboard images at the current version
The home dashboard block SHALL show a gallery of 3 to 5 dashboard images, and the dashboard product page SHALL show all 9 dashboard images, each with a version of v0.6.0 or later.

#### Scenario: Home gallery
- **WHEN** a reader opens the home page
- **THEN** the dashboard block shows 4 images in 2 columns
- **AND** 1 column at a width of 900 px or less

#### Scenario: Product page
- **WHEN** a reader opens the dashboard product page
- **THEN** the page shows 9 images, including the review dialog and Create config of the Setup tab

#### Scenario: Unknown asset id
- **WHEN** a page names an asset id that is not in `assets.json`
- **THEN** the site build stops
