# Candidate Take-Home API

Domain language for the candidate assessment API and its isolated candidate data.

## Language

**Candidate tenant**:
The isolated data space assigned to one assessment candidate, containing that candidate's Admin, Employees, and assessment records.
_Avoid_: sandbox

**Admin**:
The privileged user account in a candidate tenant that manages its Employees and tenant records.

**Employee**:
A user account in a candidate tenant representing an employee managed by that tenant's Admin.

**Deleted Employee**:
An Employee an Admin has removed from the tenant's active list. They remain that same Employee, with the same code and chat history and with login disabled, until that tenant's Admin creates an Employee with their email again.
_Avoid_: removed user, archived employee

**Conversation**:
The single chat thread between one Employee and the Admin who owns them, created together with the Employee.
_Avoid_: chat room, thread

**Authenticated session**:
A single sign-in period that can be revoked independently. Disabling an account or resetting a candidate tenant revokes all affected sessions.

**Sent message**:
A message that the server has saved successfully.

**Delivered message**:
A sent message that at least one recipient session has confirmed receiving.

**Read message**:
A delivered message that the recipient has marked as read by advancing their read position.

**Unread message**:
A message from the other participant that is later than the recipient's read position.

**Typing indicator**:
A temporary signal that a participant is typing in a conversation.

**Online participant**:
A participant with at least one active authenticated chat session.
