# Restore a Deleted Employee when create reuses their email

A Deleted Employee keeps their record, code, and chat history, so their email stayed unique and `POST /employees` returned `409`. We restore that same Employee when the create email matches a Deleted Employee in the same tenant. The create body replaces the profile and password, the avatar and created time stay, and the version increments. Inserting a new Employee would split chat history from the person the Admin just recreated. Another tenant cannot take the email, because login has no tenant selector.
