package github

// itemsQuery は Project の State のフィールドと、Issue を 1 ページぶん取る。
// %s には Project の持ち主の種類（user か organization）が入る。
const itemsQuery = `query($login: String!, $number: Int!, $field: String!, $cursor: String) {
  owner: %s(login: $login) {
    projectV2(number: $number) {
      id
      field(name: $field) {
        ... on ProjectV2SingleSelectField { id options { id name } }
      }
      items(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          state: fieldValueByName(name: $field) {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          content {
            ... on Issue {
              number
              title
              body
              url
              state
              repository { nameWithOwner }
              labels(first: 100) { nodes { name } }
              assignees(first: 100) { nodes { login } }
            }
          }
        }
      }
    }
  }
}`

const setStateMutation = `mutation($project: ID!, $item: ID!, $field: ID!, $option: String!) {
  updateProjectV2ItemFieldValue(input: {projectId: $project, itemId: $item, fieldId: $field, value: {singleSelectOptionId: $option}}) {
    projectV2Item { id }
  }
}`
